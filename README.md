# estafette-k8s-hpa-scaler
This controller can set min and max pods of a HorizontalPodAutoscaler based on a Prometheus query in order to prevent applications from scaling down if an upstream error happens

[![License](https://img.shields.io/github/license/estafette/estafette-k8s-hpa-scaler.svg)](https://github.com/estafette/estafette-k8s-hpa-scaler/blob/master/LICENSE)

## Why?

With CPU based autoscaling an application can suddenly scale down if requests start erroring and the application consumes less CPU as a result of that; to recover after tackling the source of the error the application needs to scale up again. This controller sets the minimum number of pods calculated from a Prometheus query in order to act as a safety net in these unusual circumstances.

Similar if your application is further down the call stack an error in one of the upstream applications can drop the number of requests coming into your application, again making it harder to recover after the issue is resolved. To guard yourself against those unwanted scale down actions you can use the request rate towards the outermost application as your source query to base your scale on.

## Usage

As a Kubernetes administrator, you first need to deploy the `rbac.yaml` file which set role and permissions.

```
kubectl apply -f rbac.yaml
```

Then deploy the application to Kubernetes cluster using the `kubernetes.yaml` manifest:

```
cat kubernetes.yaml | \
    PROMETHEUS_SERVER_URL=http://prometheus.monitoring.svc \
    MINIMUM_REPLICAS_LOWER_BOUND=3 \
    APP_NAME=estafette-k8s-hpa-scaler \
    NAMESPACE=estafette \
    TEAM_NAME=myteam \
    GO_PIPELINE_LABEL=1.0.5 \
    VERSION=1.0.5 \
    CPU_REQUEST=10m \
    MEMORY_REQUEST=15Mi \
    CPU_LIMIT=50m \
    MEMORY_LIMIT=128Mi \
    envsubst | kubectl apply -f -
```

Once the controller is up and running you can annotate your `HorizontalPodAutoscaler` to control the value of `minReplicas`.  
There are two ways we can use the scaler.

### Use a Prometheus query

The first option is to specify a Prometheus query which will control the minimum number of pods.  
You have to use the following annotations to specify the Prometheus query (which should usually be a query that retrieves the incoming request rate of the first API in your stack):

```yaml
apiVersion: autoscaling/v1
kind: HorizontalPodAutoscaler
metadata:
  annotations:
    estafette.io/hpa-scaler: "true"
    estafette.io/hpa-scaler-prometheus-query: "sum(rate(nginx_http_requests_total{app='my-app'}[5m])) by (app)"
    estafette.io/hpa-scaler-requests-per-replica: "2.5"
    estafette.io/hpa-scaler-delta: "-0.5"
```

With these values the following formula is used to calculate the `minReplicas` value for the `HorizontalPodAutoscaler`:

```
minReplicas = Ceiling ( delta + ( resultFromQuery / requestsPerReplica ) )
```

By tuning the `delta` and `requestsPerReplica` values it should be possible to follow the curve of the number of requests coming out of the Prometheus query closely and stay just below the number of replicas that the `HorizontalPodAutoscaler` would come up with under normal circumstances. If the curve is higher you're wasting resources, if it's much lower than it provides less safety.

### Limit the rate of scale down

It can cause problems that the built in horizontal pod auto scaler can scale down a service too quickly if the CPU load drops. There is no built-in way to limit how big portion of the current pod count the auto scaler can remove in one step.

The way the [Horizontal Pod Autoscaler](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/) works is that it periodically (by default, every 30 seconds) checks the target metric (for example the CPU-load) of the pods in our deployment, and if the pods are over or underutilized, it increases or decreases the replica count accordingly.  
What this means in practice, is if we currently have 100 replicas, the current CPU-load is 10%, and the target CPU-load is 50%, then the auto scaler calculates the new replica count the following way:

```
newReplicaCount = 100 * (10 / 50)
```

So it scales the deployment down from 100 to 20 replicas.  
Certain attributes (such as the frequency of checking the metrics, or the minimum wait time before subsequent scale downs) can be controlled globally on our cluster by passing in some flags to the controller manager. You can find more info about the possible options [here](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/).

This can cause problems for services which are sensitive to being overloaded, and need time to scale back up, because a sudden drop in the CPU load can cause a degradation.

To address this, you can set the `estafette.io/hpa-scaler-scale-down-max-ratio` annotation to control the maximum percentage of the pods that can be scaled down in one step.  
For example this is the setup to limit the maximum scale down to 20%.

```yaml
apiVersion: autoscaling/v1
kind: HorizontalPodAutoscaler
metadata:
  annotations:
    estafette.io/hpa-scaler: "true"
    estafette.io/hpa-scaler-scale-down-max-ratio: "0.2"
```

*Note*: During a rolling deployment, due to the number of replicas surging, the replica count can suddenly increase to a much larger number than how it normally is, thus the scaler can set the minimum pod count higher than it's needed.  
To avoid this, we have an experimental feature with which we don't run the pod-based scaling during a deployment. The way this is determined is we check how many `ReplicaSet`s with non-zero replica count exist for the application. If we find more than one such `ReplicaSet`s, we assume that a deployment is in progress, and the pod-based scaling is skipped.  
Keep in mind that if there will be multiple non-empty `ReplicaSet`s for any other reason (for example because you run a canary pod for an extended time period), the pod-based scaling will be skipped until only one non-empty `ReplicaSet` remains.  
To enable this behavior, you have to set the annotation `estafette.io/hpa-scaler-enable-scale-down-ratio-deployment-checking` on the HPA to `"true"`. Keep in mind that this can increase both the runtime of each iteration of the controller, and also its memory usage, because in order to do this, it has to retrieve all the ReplicaSets from the cluster.

Both the Prometheus-query and the percentage based approach work by periodically updating the `minReplicas` property of the auto scaler.  
We can use both at the same time, in that case the controller will choose the larger minimum value.
