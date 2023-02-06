module github.com/estafette/estafette-k8s-hpa-scaler

go 1.12

require (
	github.com/alecthomas/kingpin v2.2.6+incompatible
	github.com/estafette/estafette-foundation v0.0.68
	github.com/prometheus/client_golang v0.9.2
	github.com/rs/zerolog v1.17.2
	github.com/sethgrid/pester v1.1.0
	github.com/stretchr/testify v1.6.1
	k8s.io/api v0.20.0
	k8s.io/apimachinery v0.20.0
	k8s.io/client-go v0.20.0
)
