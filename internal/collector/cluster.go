package collector

import (
	"context"
	"net/http"
	"strings"
	"time"

	"server-monitor/internal/parser"
)

// ElasticsearchPayload: clusters.elasticsearch w GET /api/health.
type ElasticsearchPayload struct {
	Status              string  `json:"status"`
	ClusterName         string  `json:"cluster_name"`
	ClusterStatus       string  `json:"cluster_status"`
	Nodes               int     `json:"nodes"`
	UnassignedShards    int     `json:"unassigned_shards"`
	ActiveShardsPercent float64 `json:"active_shards_percent"`
	Error               string  `json:"error,omitempty"`
}

// Elasticsearch sonduje GET {URL}/_cluster/health.
type Elasticsearch struct {
	URL    string
	Auth   HTTPAuth
	Client *http.Client
}

func (p *Elasticsearch) Key() string { return "elasticsearch" }

func (p *Elasticsearch) Check(ctx context.Context) Result {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	body, err := httpGet(ctx, p.Client, strings.TrimRight(p.URL, "/")+"/_cluster/health", p.Auth)
	if err != nil {
		return Result{"CRIT", ElasticsearchPayload{Status: "CRIT", Error: err.Error()}}
	}
	h, err := parser.ParseESHealth(body)
	if err != nil {
		return Result{"CRIT", ElasticsearchPayload{Status: "CRIT", Error: err.Error()}}
	}
	return Result{h.Status, ElasticsearchPayload{Status: h.Status, ClusterName: h.ClusterName,
		ClusterStatus: h.ClusterStatus, Nodes: h.Nodes, UnassignedShards: h.UnassignedShards,
		ActiveShardsPercent: h.ActiveShardsPercent}}
}

// KibanaPayload: clusters.kibana w GET /api/health.
type KibanaPayload struct {
	Status       string `json:"status"`
	OverallLevel string `json:"overall_level"`
	ResponseMS   int64  `json:"response_ms"`
	Error        string `json:"error,omitempty"`
}

// Kibana sonduje GET {URL}/api/status.
type Kibana struct {
	URL    string
	Auth   HTTPAuth
	Client *http.Client
}

func (p *Kibana) Key() string { return "kibana" }

func (p *Kibana) Check(ctx context.Context) Result {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	start := time.Now()
	body, err := httpGet(ctx, p.Client, strings.TrimRight(p.URL, "/")+"/api/status", p.Auth)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return Result{"CRIT", KibanaPayload{Status: "CRIT", ResponseMS: ms, Error: err.Error()}}
	}
	k, err := parser.ParseKibanaStatus(body)
	if err != nil {
		return Result{"CRIT", KibanaPayload{Status: "CRIT", ResponseMS: ms, Error: err.Error()}}
	}
	return Result{k.Status, KibanaPayload{Status: k.Status, OverallLevel: k.OverallLevel, ResponseMS: ms}}
}
