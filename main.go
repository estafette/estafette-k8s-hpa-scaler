package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"math/rand"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/alecthomas/kingpin"
	foundation "github.com/estafette/estafette-foundation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/sethgrid/pester"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
)

const annotationHPAScaler = "estafette.io/hpa-scaler"
const annotationHPAScalerPrometheusQuery = "estafette.io/hpa-scaler-prometheus-query"
const annotationHPAScalerRequestsPerReplica = "estafette.io/hpa-scaler-requests-per-replica"
const annotationHPAScalerDelta = "estafette.io/hpa-scaler-delta"
const annotationHPAScalerPrometheusServerURL = "estafette.io/hpa-scaler-prometheus-server-url"
const annotationHPAScalerScaleDownMaxRatio = "estafette.io/hpa-scaler-scale-down-max-ratio"
const annotationHPAScalerEnableScaleDownRatioDeploymentChecking = "estafette.io/hpa-scaler-enable-scale-down-ratio-deployment-checking"

const annotationHPAScalerState = "estafette.io/hpa-scaler-state"

// HPAScalerState represents the state of the HorizontalPodAutoscaler with respect to the Estafette k8s hpa scaler
type HPAScalerState struct {
	Enabled                                string  `json:"enabled"`
	PrometheusQuery                        string  `json:"prometheusQuery"`
	RequestsPerReplica                     float64 `json:"requestsPerReplica"`
	Delta                                  float64 `json:"delta"`
	LastUpdated                            string  `json:"lastUpdated"`
	PrometheusServerURL                    string  `json:"prometheusServerUrl"`
	ScaleDownMaxRatio                      float64 `json:"scaleDownMaxRatio"`
	EnableScaleDownRatioDeploymentChecking string  `json:"enableScaleDownRatioDeploymentChecking"`
}

type replicaSetsHolder struct {
	replicaSetList *appsv1.ReplicaSetList
}

var (
	appgroup  string
	app       string
	version   string
	branch    string
	revision  string
	buildDate string
	goVersion = runtime.Version()
)

var (
	prometheusServerURL = kingpin.Flag("prometheus-server-url", "The url to reach the Prometheus server.").Envar("PROMETHEUS_SERVER_URL").Required().String()

	// seed random number
	r = rand.New(rand.NewSource(time.Now().UnixNano()))

	// define prometheus counter
	hpaTotals = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "estafette_hpa_scaler_totals",
			Help: "Number of processed HorizontalPodAutoscalers.",
		},
		[]string{"namespace", "status", "initiator"},
	)

	// create gauge for tracking minimum number of replicas per hpa
	minReplicasVector = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "estafette_hpa_scaler_min_replicas",
		Help: "The minimum number of replicas per hpa as set by this application.",
	}, []string{"hpa", "namespace"})

	// create gauge for tracking actual number of replicas per hpa
	actualReplicasVector = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "estafette_hpa_scaler_actual_replicas",
		Help: "The actual number of replicas per hpa as set by this application.",
	}, []string{"hpa", "namespace"})

	// create gauge for tracking request rate used to set minimum number of replicas per hpa
	requestRateVector = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "estafette_hpa_scaler_request_rate",
		Help: "The request rate used for setting minimum number of replicas per hpa as set by this application.",
	}, []string{"hpa", "namespace"})
)

func init() {
	// metrics have to be registered to be exposed
	prometheus.MustRegister(hpaTotals)
	prometheus.MustRegister(minReplicasVector)
	prometheus.MustRegister(actualReplicasVector)
	prometheus.MustRegister(requestRateVector)
}

func main() {
	// parse command line parameters
	kingpin.Parse()

	// init log format from envvar ESTAFETTE_LOG_FORMAT
	foundation.InitLoggingFromEnv(foundation.NewApplicationInfo(appgroup, app, version, branch, revision, buildDate))

	// init /liveness endpoint
	foundation.InitLiveness()

	// creates the in-cluster config
	kubeClientConfig, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed getting in-cluster kubernetes config")
	}
	// creates the kubernetes clientset
	k8sClient, err := kubernetes.NewForConfig(kubeClientConfig)

	if err != nil {
		log.Fatal().Err(err).Msg("Failed creating kubernetes clientset")
	}

	foundation.InitMetrics()

	gracefulShutdown, waitGroup := foundation.InitGracefulShutdownHandling()

	go func(waitGroup *sync.WaitGroup) {
		// loop indefinitely
		for {

			log.Info().Msg("Listing horizontal pod autoscalers for all namespaces...")
			hpas, err := k8sClient.AutoscalingV1().HorizontalPodAutoscalers("").List(metav1.ListOptions{})
			replicaSets := &replicaSetsHolder{replicaSetList: nil}

			if err != nil {
				log.Error().Err(err).Msg("Could not list the horizontal pod autoscalers in the cluster.")
			} else {
				log.Info().Msgf("Cluster has %v horizontal pod autoscalers", len(hpas.Items))

				// loop all hpas
				if hpas.Items != nil {
					for _, hpa := range hpas.Items {
						waitGroup.Add(1)
						status, err := processHorizontalPodAutoscaler(k8sClient, &hpa, replicaSets, "poller")
						hpaTotals.With(prometheus.Labels{"namespace": hpa.Namespace, "status": status, "initiator": "poller"}).Inc()
						waitGroup.Done()

						if err != nil {
							log.Warn().Err(err).Msg("")
							continue
						}
					}
				}
			}

			// sleep random time around 90 seconds
			sleepTime := applyJitter(90)
			log.Info().Msgf("Sleeping for %v seconds...", sleepTime)
			time.Sleep(time.Duration(sleepTime) * time.Second)
		}
	}(waitGroup)

	foundation.HandleGracefulShutdown(gracefulShutdown, waitGroup)
}

func processHorizontalPodAutoscaler(kubeClient *kubernetes.Clientset, hpa *autoscalingv1.HorizontalPodAutoscaler, replicaSets *replicaSetsHolder, initiator string) (status string, err error) {
	if hpa != nil && hpa.Annotations != nil {
		desiredState := getDesiredHorizontalPodAutoscalerState(hpa)

		status, err := makeHorizontalPodAutoscalerChanges(kubeClient, hpa, replicaSets, initiator, desiredState)

		return status, err
	}

	return "skipped", nil
}

func getDesiredHorizontalPodAutoscalerState(hpa *autoscalingv1.HorizontalPodAutoscaler) (state HPAScalerState) {
	var ok bool

	// get annotations or set default value
	state.Enabled, ok = hpa.Annotations[annotationHPAScaler]
	if !ok {
		state.Enabled = "false"
	}

	state.PrometheusQuery, ok = hpa.Annotations[annotationHPAScalerPrometheusQuery]
	if !ok {
		state.PrometheusQuery = ""
	}

	requestsPerReplicaString, ok := hpa.Annotations[annotationHPAScalerRequestsPerReplica]
	if !ok {
		state.RequestsPerReplica = 1
	} else {
		i, err := strconv.ParseFloat(requestsPerReplicaString, 64)
		if err == nil {
			state.RequestsPerReplica = i
		} else {
			state.RequestsPerReplica = 1
		}
	}

	deltaString, ok := hpa.Annotations[annotationHPAScalerDelta]
	if !ok {
		state.Delta = 0
	} else {
		i, err := strconv.ParseFloat(deltaString, 64)
		if err == nil {
			state.Delta = i
		} else {
			state.Delta = 0
		}
	}

	prometheusServerURLState, ok := hpa.Annotations[annotationHPAScalerPrometheusServerURL]
	if !ok {
		prometheusServerURLState = *prometheusServerURL
	}

	state.PrometheusServerURL = prometheusServerURLState

	scaleDownMaxRatioString, ok := hpa.Annotations[annotationHPAScalerScaleDownMaxRatio]
	if !ok {
		state.ScaleDownMaxRatio = 1
	} else {
		i, err := strconv.ParseFloat(scaleDownMaxRatioString, 64)
		if err == nil {
			state.ScaleDownMaxRatio = i
		} else {
			state.ScaleDownMaxRatio = 1
		}
	}

	state.EnableScaleDownRatioDeploymentChecking, ok = hpa.Annotations[annotationHPAScalerEnableScaleDownRatioDeploymentChecking]
	if !ok {
		state.EnableScaleDownRatioDeploymentChecking = "false"
	}

	return
}

func makeHorizontalPodAutoscalerChanges(kubeClient *kubernetes.Clientset, hpa *autoscalingv1.HorizontalPodAutoscaler, replicaSets *replicaSetsHolder, initiator string, desiredState HPAScalerState) (status string, err error) {
	status = "failed"

	// check if hpa-scaler is enabled for this hpa and query is not empty and requests per replica larger than zero
	if desiredState.Enabled == "true" {
		minimumReplicasLowerBoundString := os.Getenv("MINIMUM_REPLICAS_LOWER_BOUND")
		minimumReplicasLowerBound := int32(3)
		if i, err := strconv.ParseInt(minimumReplicasLowerBoundString, 0, 32); err == nil {
			minimumReplicasLowerBound = int32(i)
		}

		minPodCountBasedOnPrometheusQuery, requestRate, err := getMinPodCountBasedOnPrometheusQuery(kubeClient, hpa, desiredState)

		if err != nil {
			return status, err
		}

		minPodCountBasedOnCurrentPodCount := minPodCountBasedOnPrometheusQuery

		deploymentInProgress := false

		if desiredState.EnableScaleDownRatioDeploymentChecking == "true" {
			// We only actually check if a deployment is in progress if this feature is explicitly enabled with an annotation.
			deploymentInProgress = isDeploymentInProgress(kubeClient, hpa, replicaSets)
		}

		if !deploymentInProgress {
			minPodCountBasedOnCurrentPodCount = getMinPodCountBasedOnCurrentPodCount(kubeClient, hpa, desiredState)
		}

		log.Debug().
			Float64("requestRate", requestRate).
			Int32("minPodCountBasedOnPrometheusQuery", minPodCountBasedOnPrometheusQuery).
			Int32("minPodCountBasedOnCurrentPodCount", minPodCountBasedOnCurrentPodCount).
			Float64("desiredState.RequestsPerReplica", desiredState.RequestsPerReplica).
			Float64("desiredState.Delta", desiredState.Delta).
			Float64("desiredState.ScaleDownMaxRatio", desiredState.ScaleDownMaxRatio).
			Float64("requestRate/desiredState.RequestsPerReplica", requestRate/desiredState.RequestsPerReplica).
			Float64("desiredState.Delta + requestRate/desiredState.RequestsPerReplica", desiredState.Delta+requestRate/desiredState.RequestsPerReplica).
			Float64("math.Ceil(desiredState.Delta + requestRate/desiredState.RequestsPerReplica)", math.Ceil(desiredState.Delta+requestRate/desiredState.RequestsPerReplica)).
			Int32("int32(math.Ceil(desiredState.Delta + requestRate/desiredState.RequestsPerReplica))", int32(math.Ceil(desiredState.Delta+requestRate/desiredState.RequestsPerReplica))).
			Int32("int32(math.Floor(float64(*hpa.Status.CurrentReplicas) * desiredState.ScaleDownMaxRatio))", int32(math.Floor(float64(hpa.Status.CurrentReplicas)*desiredState.ScaleDownMaxRatio))).
			Msgf("Calculated values for hpa %v in namespace %v", hpa.Name, hpa.Namespace)

		// We pick the larger minimum of the two.
		targetNumberOfMinReplicas := minPodCountBasedOnPrometheusQuery
		if minPodCountBasedOnCurrentPodCount > targetNumberOfMinReplicas {
			targetNumberOfMinReplicas = minPodCountBasedOnCurrentPodCount
		}

		// We only override the minimum pod count if we don't go below the hard-coded minimum.
		if targetNumberOfMinReplicas < minimumReplicasLowerBound {
			targetNumberOfMinReplicas = minimumReplicasLowerBound
		}

		currentNumberOfMinReplicas := *hpa.Spec.MinReplicas
		actualNumberOfReplicas := hpa.Status.CurrentReplicas

		// set prometheus gauge values
		minReplicasVector.WithLabelValues(hpa.Name, hpa.Namespace).Set(float64(targetNumberOfMinReplicas))
		actualReplicasVector.WithLabelValues(hpa.Name, hpa.Namespace).Set(float64(actualNumberOfReplicas))
		requestRateVector.WithLabelValues(hpa.Name, hpa.Namespace).Set(requestRate)

		if targetNumberOfMinReplicas == currentNumberOfMinReplicas {
			// don't update hpa
			return "skipped", nil
		}

		// update hpa
		log.Info().Msgf("[%v] HorizontalPodAutosclaler %v.%v - Updating hpa because minReplicas has changed from %v to %v...", initiator, hpa.Name, hpa.Namespace, currentNumberOfMinReplicas, targetNumberOfMinReplicas)

		// serialize state and store it in the annotation
		desiredState.LastUpdated = time.Now().Format(time.RFC3339)
		hpaScalerStateByteArray, err := json.Marshal(desiredState)
		if err != nil {
			log.Error().Err(err).Msg("")
			return status, err
		}
		hpa.Annotations[annotationHPAScalerState] = string(hpaScalerStateByteArray)
		hpa.Spec.MinReplicas = &targetNumberOfMinReplicas

		if *hpa.Spec.MinReplicas >= hpa.Spec.MaxReplicas {
			targetNumberOfMaxReplicas := *hpa.Spec.MinReplicas + int32(1)
			hpa.Spec.MaxReplicas = targetNumberOfMaxReplicas
		}

		// update hpa, because the data and state annotation have changed
		hpa, err = kubeClient.AutoscalingV1().HorizontalPodAutoscalers(hpa.Namespace).Update(hpa)
		if err != nil {
			log.Error().Err(err).Msg("")
			return status, err
		}

		status = "succeeded"

		log.Info().Msgf("[%v] HorizontalPodAutosclaler %v.%v - Updated hpa successfully...", initiator, hpa.Name, hpa.Namespace)

		return status, nil
	}

	status = "skipped"

	return status, nil
}

// Returns what the minimum pod count should be based on the Prometheus query specified
// If the Prometheus query is not specified, it returns 0
func getMinPodCountBasedOnPrometheusQuery(kubeClient *kubernetes.Clientset, hpa *autoscalingv1.HorizontalPodAutoscaler, desiredState HPAScalerState) (minPodCount int32, requestRate float64, err error) {
	minPodCount = 0
	requestRate = 0

	if len(desiredState.PrometheusQuery) > 0 && desiredState.RequestsPerReplica > 0 {
		// get request rate with prometheus query
		// http://prometheus.production.svc/api/v1/query?query=sum%28rate%28nginx_http_requests_total%7Bhost%21~%22%5E%28%3F%3A%5B0-9.%5D%2B%29%24%22%2Clocation%3D%22%40searchfareapi_gcloud%22%7D%5B10m%5D%29%29%20by%20%28location%29
		prometheusQueryURL := fmt.Sprintf("%v/api/v1/query?query=%v", desiredState.PrometheusServerURL, url.QueryEscape(desiredState.PrometheusQuery))
		resp, err := pester.Get(prometheusQueryURL)
		if err != nil {
			log.Error().Err(err).Msgf("Executing prometheus query for hpa %v in namespace %v failed", hpa.Name, hpa.Namespace)
			return 0, 0, err
		}

		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Error().Err(err).Msgf("Reading prometheus query response body for hpa %v in namespace %v failed", hpa.Name, hpa.Namespace)
			return 0, 0, err
		}

		queryResponse, err := UnmarshalPrometheusQueryResponse(body)
		if err != nil {
			log.Error().Err(err).Msgf("Unmarshalling prometheus query response body for hpa %v in namespace %v failed", hpa.Name, hpa.Namespace)
			return 0, 0, err
		}

		requestRate, err = queryResponse.GetRequestRate()
		if err != nil {
			log.Error().Err(err).Msgf("Retrieving request rate from query response body for hpa %v in namespace %v failed", hpa.Name, hpa.Namespace)
			return 0, 0, err
		}

		// calculate target # of replicas
		minPodCount = int32(math.Ceil(desiredState.Delta + requestRate/desiredState.RequestsPerReplica))
	}

	return minPodCount, requestRate, nil
}

// Returns what the minimum pod count should be based on the current pod count and the maximum scale down ratio
func getMinPodCountBasedOnCurrentPodCount(kubeClient *kubernetes.Clientset, hpa *autoscalingv1.HorizontalPodAutoscaler, desiredState HPAScalerState) (podCount int32) {
	actualNumberOfReplicas := hpa.Status.CurrentReplicas

	// We use Floor() because we want to opt on the side of scaling down slower.
	maxScaleDown := int32(math.Floor(float64(actualNumberOfReplicas) * desiredState.ScaleDownMaxRatio))

	// If the (number of replicas) * (scale down max ratio) is zero, that would completely prevent scaling down, which we don't want.
	if maxScaleDown == 0 {
		return actualNumberOfReplicas - 1
	}

	return actualNumberOfReplicas - maxScaleDown
}

// Returns whether the application associated with the HPA is being deployed right now. (We consider an application being deployed if it has more than one non empty replicasets.)
func isDeploymentInProgress(kubeClient *kubernetes.Clientset, hpa *autoscalingv1.HorizontalPodAutoscaler, replicaSets *replicaSetsHolder) bool {
	app := hpa.Labels["app"]

	if replicaSets.replicaSetList == nil {
		replicaSets.replicaSetList = getReplicaSets(kubeClient)
	}

	var replicaSetsForApp []*appsv1.ReplicaSet

	for _, rs := range replicaSets.replicaSetList.Items {
		if rs.Labels["app"] == app {
			replicaSetsForApp = append(replicaSetsForApp, &rs)
		}
	}

	nonEmptyReplicaSetCount := 0

	for _, rs := range replicaSetsForApp {
		if rs.Status.Replicas > 0 {
			nonEmptyReplicaSetCount++
		}
	}

	return nonEmptyReplicaSetCount > 1
}

// Retrieves all the replica sets present in the cluster.
func getReplicaSets(kubeClient *kubernetes.Clientset) *appsv1.ReplicaSetList {
	log.Info().Msg("Listing replicasets for all namespaces...")
	replicaSets, err := kubeClient.AppsV1().ReplicaSets("").List(metav1.ListOptions{})

	if err != nil {
		log.Error().Err(err).Msg("Could not list the replicasets in the cluster.")
	}

	log.Info().Msgf("Cluster has %v replicasets", len(replicaSets.Items))
	return replicaSets
}

func applyJitter(input int) (output int) {
	deviation := int(0.25 * float64(input))

	return input - deviation + r.Intn(2*deviation)
}
