package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnmarshalPrometheusQueryResponse(t *testing.T) {

	t.Run("ReturnsUnmarshalledResponse", func(t *testing.T) {

		responseBody := []byte("{\"status\":\"success\",\"data\":{\"resultType\":\"vector\",\"result\":[{\"metric\":{\"location\":\"@searchfareapi_gcloud\"},\"value\":[1513161148.757,\"225.4068155675859\"]}]}}")

		// act
		queryResponse, err := UnmarshalPrometheusQueryResponse(responseBody)

		assert.Nil(t, err)
		assert.Equal(t, "success", queryResponse.Status)
		assert.Equal(t, "vector", queryResponse.Data.ResultType)
		//assert.Equal(t, "{\"location\":\"@searchfareapi_gcloud\"}", queryResponse.Data.Result[0].Metric)
		assert.Equal(t, "225.4068155675859", queryResponse.Data.Result[0].Value[1])
	})
}

func TestGetRequestRate(t *testing.T) {

	t.Run("ReturnsQueryValueAsFloat64", func(t *testing.T) {

		queryResponse := PrometheusQueryResponse{
			Data: PrometheusQueryResponseData{
				Result: []PrometheusQueryResponseDataResult{
					PrometheusQueryResponseDataResult{
						Value: []interface{}{
							1513161148.757,
							"225.4068155675859",
						},
					},
				},
			},
		}

		// act
		floatValue, err := queryResponse.GetRequestRate()

		assert.Nil(t, err)
		assert.Equal(t, 225.4068155675859, floatValue)
	})
}
