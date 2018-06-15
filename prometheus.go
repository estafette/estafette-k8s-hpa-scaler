package main

import (
	"encoding/json"
	"strconv"

	"github.com/rs/zerolog/log"
)

// PrometheusQueryResponseDataResult is used to unmarshal the response from a prometheus query
// {"metric":{"location":"@searchfareapi_gcloud"},"value":[1513161148.757,"225.4068155675859"]}
type PrometheusQueryResponseDataResult struct {
	Metric interface{}   `json:"metric"`
	Value  []interface{} `json:"value"`
}

// PrometheusQueryResponseData is used to unmarshal the response from a prometheus query
type PrometheusQueryResponseData struct {
	ResultType string                              `json:"resultType"`
	Result     []PrometheusQueryResponseDataResult `json:"result"`
}

// PrometheusQueryResponse is used to unmarshal the response from a prometheus query
type PrometheusQueryResponse struct {
	Status string                      `json:"status"`
	Data   PrometheusQueryResponseData `json:"data"`
}

//sum(rate(nginx_http_requests_total{host!~"^(?:[0-9.]+)$",location="@searchfareapi_gcloud"}[10m])) by (location)
// {"status":"success","data":{"resultType":"vector","result":[{"metric":{"location":"@searchfareapi_gcloud"},"value":[1513161148.757,"225.4068155675859"]}]}}%

// UnmarshalPrometheusQueryResponse unmarshals the response for a prometheus query
func UnmarshalPrometheusQueryResponse(responseBody []byte) (queryResponse PrometheusQueryResponse, err error) {

	if err = json.Unmarshal(responseBody, &queryResponse); err != nil {
		log.Error().Err(err).Msg("Failed unmarshalling prometheus query response")
		return
	}

	log.Debug().Interface("queryResponse", queryResponse).Msg("Successfully unmarshalled prometheus query response")

	return
}

// GetRequestRate converts the string value into a float64
func (pqr *PrometheusQueryResponse) GetRequestRate() (float64, error) {
	f, err := strconv.ParseFloat(pqr.Data.Result[0].Value[1].(string), 64)

	return f, err
}
