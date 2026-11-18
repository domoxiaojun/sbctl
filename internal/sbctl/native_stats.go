package sbctl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

type nativeStatsCounter struct {
	Tag           string `json:"tag"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

type nativeStatsResponse struct {
	SchemaVersion int    `json:"schema_version"`
	NodeID        string `json:"node_id"`
	RuntimeEpoch  string `json:"runtime_epoch"`
	Sequence      uint64 `json:"sequence"`
	SampledAt     string `json:"sampled_at"`
	Counters      *struct {
		UploadBytes   int64 `json:"upload_bytes"`
		DownloadBytes int64 `json:"download_bytes"`
	} `json:"counters"`
	Inbounds             []nativeStatsCounter    `json:"inbounds"`
	Outbounds            []nativeStatsCounter    `json:"outbounds"`
	LocalUsers           []nativeStatsCounter    `json:"local_users"`
	Connections          []nativeStatsConnection `json:"connections"`
	ConnectionSequence   uint64                  `json:"connection_sequence"`
	ConnectionsTruncated bool                    `json:"connections_truncated"`
	More                 bool                    `json:"more"`
	Error                string                  `json:"error"`
}

type nativeStatsConnection struct {
	Sequence      uint64   `json:"sequence"`
	ID            string   `json:"id"`
	Inbound       string   `json:"inbound"`
	InboundType   string   `json:"inbound_type"`
	Network       string   `json:"network"`
	Domain        string   `json:"domain"`
	Destination   string   `json:"destination"`
	Source        string   `json:"source"`
	Outbound      string   `json:"outbound"`
	OutboundType  string   `json:"outbound_type"`
	Chain         []string `json:"chain"`
	Rule          string   `json:"rule"`
	UploadBytes   int64    `json:"upload_bytes"`
	DownloadBytes int64    `json:"download_bytes"`
	StartedAt     string   `json:"started_at"`
	ClosedAt      string   `json:"closed_at"`
}

func nativeStatsSocket() string {
	if path := os.Getenv("SBCTL_STATS_SOCKET"); path != "" {
		return path
	}
	return nodeStatsDir + "/collector.sock"
}

func readNativeStats(action string) nativeStatsResponse {
	return readNativeStatsSince(action, "", 0)
}

func readNativeStatsSince(action, epoch string, after uint64) nativeStatsResponse {
	conn, err := net.DialTimeout("unix", nativeStatsSocket(), 3*time.Second)
	check(err)
	defer conn.Close()
	check(conn.SetDeadline(time.Now().Add(5 * time.Second)))
	check(json.NewEncoder(conn).Encode(M{"action": action, "runtime_epoch": epoch, "after_sequence": after, "limit": connectionPageSize}))
	var response nativeStatsResponse
	check(json.NewDecoder(bufio.NewReader(io.LimitReader(conn, MaxDocument))).Decode(&response))
	if response.SchemaVersion != 1 || response.RuntimeEpoch == "" || response.SampledAt == "" || response.Error != "" {
		fail("STATS_PROTOCOL", "自研统计接口回执无效", 503)
	}
	return response
}

func nativeStatsRows(response nativeStatsResponse, manifest *StatisticsManifest) []M {
	rows := []M{}
	add := func(scope string, expected []string, values []nativeStatsCounter) {
		for _, item := range values {
			if !contains(expected, item.Tag) {
				continue
			}
			rows = append(rows, M{"name": scope + ">>>" + item.Tag + ">>>traffic>>>uplink", "value": item.UploadBytes})
			rows = append(rows, M{"name": scope + ">>>" + item.Tag + ">>>traffic>>>downlink", "value": item.DownloadBytes})
		}
	}
	inbounds := []string{}
	for _, item := range manifest.Inbounds {
		inbounds = append(inbounds, item.Tag)
	}
	add("inbound", inbounds, response.Inbounds)
	add("outbound", manifest.Outbounds, response.Outbounds)
	add("user", manifest.LocalUsers, response.LocalUsers)
	return rows
}

func nativeStatsConnections(response nativeStatsResponse) []TrafficConnection {
	result := make([]TrafficConnection, 0, len(response.Connections))
	for _, c := range response.Connections {
		result = append(result, TrafficConnection{Sequence: c.Sequence, ID: c.ID, Inbound: c.Inbound, InboundType: c.InboundType, Network: c.Network, Domain: c.Domain, Destination: c.Destination, Source: c.Source, Outbound: c.Outbound, OutboundType: c.OutboundType, Chain: append([]string{}, c.Chain...), Rule: c.Rule, Upload: fmt.Sprint(c.UploadBytes), Download: fmt.Sprint(c.DownloadBytes), StartedAt: c.StartedAt, ClosedAt: c.ClosedAt})
	}
	return result
}
