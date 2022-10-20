package sock2rtm

import (
	"encoding/json"
	"net/http"
)

type Metrics struct {
	Slack struct {
		Hello      int64 `json:"hello"`
		Connecting int64 `json:"connecting"`
		Connected  int64 `json:"connected"`
		Disconnect int64 `json:"disconnect"`
	} `json:"slack"`
	WebSocket struct {
		TotalConnections   int64 `json:"total_connections"`
		CurrentConnections int64 `json:"current_connections"`
	} `json:"websocket"`
	Messages struct {
		Received     int64 `json:"received_from_slack"`
		Delivered    int64 `json:"delivered_to_websocket"`
		Unsupported  int64 `json:"unsupported_from_slack"`
		WriteErrored int64 `json:"write_errored_to_websocket"`
	} `json:"messages"`
}

func (app *App) metricsFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(app.metrics)
}
