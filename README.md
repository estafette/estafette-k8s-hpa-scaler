# estafette-k8s-hpa-scaler
This controller can set min and max pods of a HorizontalPodAutoscaler based on a prometheus query in order to prevent applications from scaling down if an upstream error happens


[![License](https://img.shields.io/github/license/estafette/estafette-k8s-hpa-scaler.svg)](https://github.com/estafette/estafette-k8s-hpa-scaler/blob/master/LICENSE)

## Why?

With cpu based autoscaling an application can suddenly scale down if requests start erroring and the application consumes less cpu as a result of that; to recover after tackling the source of the error the application needs to scale up again. This controller sets the minimum number of pods calculated from a Prometheus query in order to act as a safety net in these unusual circumstances.

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

Once the controller is up and running you can annotate your `HorizontalPodAutoscaler` as follows to make the `minReplicas` follow the request rate retrieved by the Prometheus query:

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