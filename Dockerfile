FROM scratch

LABEL maintainer="estafette.io" \
      description="The estafette-k8s-hpa-scaler is a Kubernetes controller that can set min and max pods of a HorizontalPodAutoscaler based on a prometheus query in order to prevent applications from scaling down if an upstream error happens"

COPY ca-certificates.crt /etc/ssl/certs/
COPY estafette-k8s-hpa-scaler /

ENTRYPOINT ["/estafette-k8s-hpa-scaler"]
