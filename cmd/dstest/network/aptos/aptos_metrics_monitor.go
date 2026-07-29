package aptos

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var interestingMetrics = map[string]struct{}{
	// RoundTimeoutCountFitness
	"aptos_consensus_timeout_count":                     {},
	"aptos_consensus_timeout_rounds_count":              {},
	"aptos_consensus_agg_round_timeout_reason":          {},
	"aptos_consensus_pending_round_timeouts":            {},
	"aptos_consensus_current_round_timeout_voted_power": {},
	"aptos_consensus_current_round_quorum_voting_power": {},

	// VoteFragmentationFitness
	"aptos_proposer_collecting_round_count":                 {},
	"aptos_proposer_collected_most_voting_power_sum":        {},
	"aptos_proposer_collected_conflicting_voting_power_sum": {},
	"aptos_proposer_collected_timeout_voting_power_sum":     {},
	"aptos_consensus_current_round_voted_power":             {},
	"aptos_consensus_current_round":                         {},
	"aptos_consensus_committed_blocks_count":                {},

	// QuorumStoreFitness
	"aptos_consensus_proposal_payload_availability_count":    {},
	"aptos_consensus_proposal_payload_batch_availability":    {},
	"aptos_consensus_proposal_payload_fetch_duration_bucket": {},
	"aptos_consensus_proposal_payload_fetch_duration_sum":    {},
	"aptos_consensus_proposal_payload_fetch_duration_count":  {},
	"aptos_consensus_quorum_store_batch_ready_count":         {},

	"quorum_store_pos_to_pull_bucket":   {},
	"quorum_store_pos_to_pull_sum":      {},
	"quorum_store_pos_to_pull_count":    {},
	"quorum_store_pos_to_commit_bucket": {},
	"quorum_store_pos_to_commit_sum":    {},
	"quorum_store_pos_to_commit_count":  {},

	"quorum_store_num_total_txns_left_on_update_sum":               {},
	"quorum_store_num_total_txns_left_on_update_count":             {},
	"quorum_store_num_total_proofs_left_on_update_sum":             {},
	"quorum_store_num_total_proofs_left_on_update_count":           {},
	"quorum_store_num_proofs_left_in_proof_queue_after_pull_sum":   {},
	"quorum_store_num_proofs_left_in_proof_queue_after_pull_count": {},
	"quorum_store_num_txns_left_in_proof_queue_after_pull_sum":     {},
	"quorum_store_num_txns_left_in_proof_queue_after_pull_count":   {},

	"quorum_store_batch_in_progress_timeout":      {},
	"quorum_store_batch_in_progress_expired":      {},
	"quorum_store_num_proofs_expired_when_commit": {},
	"quorum_store_batch_num_per_block_sum":        {},
	"quorum_store_batch_num_per_block_count":      {},
}

type MetricsMonitor struct {
	finishSignal chan struct{}
	contentChan  chan MetricsContent

	metricsLog  *csv.Writer
	metricsFile *os.File
}

type MetricsContent struct {
	NodeId     int
	LogTime    int64
	Metric     string
	LabelsJSON string
	Value      float64
}

func (m *MetricsMonitor) Put(content MetricsContent) {
	select {
	case m.contentChan <- content:
	case <-m.finishSignal:
	}
}

/*
The metrics monitor periodically polls the inspection service of each node for metrics information.
*/
func StartMetricsMonitor(
	outputDir, testName, schedulerType string,
	iteration int,
	numReplicas, basePort int,
) (*MetricsMonitor, error) {
	quit := make(chan struct{})

	monitor := &MetricsMonitor{finishSignal: quit, contentChan: make(chan MetricsContent, 1024)}
	monitor.Init(outputDir, testName, schedulerType, iteration)

	go monitor.Monitor()
	for i := 0; i < numReplicas; i++ {
		go monitor.PollForNode(i, basePort+100+i*10+6)
	}

	return monitor, nil
}

/*
PollForNode continously polls the given node (once per second) for its latest metrics info and sends it to the monitor.
It makes a HTTP GET request to /metrics, reads the body text, parses every metric line and keeps only the metrics
we are interested in.
It sends one MetricsContent per metric sample on the content channel.
*/
func (m *MetricsMonitor) PollForNode(nodeID, port int) {
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://localhost:%d/metrics", port)

	for ; ; time.Sleep(time.Millisecond * 1000) {
		select {
		case <-m.finishSignal:
			return
		default:
			resp, err := client.Get(url)
			if err != nil {
				fmt.Printf("[Metrics port: %d] Error fetching metrics: %s\n", port, err)
				continue
			}

			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK {
				continue
			}

			logTime := time.Now().UnixMicro()

			for _, line := range strings.Split(string(body), "\n") {
				metric, labels, value, ok := parsePrometheusLine(line)
				if !ok {
					continue
				}

				if _, keep := interestingMetrics[metric]; !keep {
					continue
				}

				labelsBytes, _ := json.Marshal(labels)

				m.Put(MetricsContent{
					NodeId:     nodeID,
					LogTime:    logTime,
					Metric:     metric,
					LabelsJSON: string(labelsBytes),
					Value:      value,
				})
			}
		}
	}

}

/*
This function monitors whenever new content arrives on the channel.
The metric is saved in metrics.csv.
*/
func (m *MetricsMonitor) Monitor() {
	for {
		select {
		case <-m.finishSignal:
			return
		case content := <-m.contentChan:
			m.metricsLog.Write([]string{
				fmt.Sprintf("%d", content.NodeId),
				fmt.Sprintf("%d", content.LogTime),
				content.Metric,
				content.LabelsJSON,
				fmt.Sprintf("%g", content.Value),
			})
			m.metricsLog.Flush()
		}
	}
}

/*
Stop signals the poller to shut down cleanly and waits for it to exit.
*/
func (m *MetricsMonitor) Stop() {
	close(m.finishSignal)
	m.closeAndFlushFiles()
}

func (m *MetricsMonitor) closeAndFlushFiles() {
	if m.metricsFile != nil {
		m.metricsLog.Flush()
		m.metricsFile.Close()
	}
}

/*
Init initializes the metrics monitor by creating the necessary metrics.csv log file for the given test iteration,
which includes all the metrics we are interested in for all replicas.
*/
func (m *MetricsMonitor) Init(
	outputDir, testName, schedulerType string,
	iteration int,
) {
	m.closeAndFlushFiles()

	path := filepath.Join(outputDir,
		fmt.Sprintf("%s_%s_%d",
			testName,
			schedulerType,
			iteration),
		"metrics.csv")

	os.MkdirAll(filepath.Dir(path), os.ModePerm)

	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("Failed to create metrics CSV %v", err)
	} else {
		m.metricsFile = f
		m.metricsLog = csv.NewWriter(f)
		m.metricsLog.Write([]string{
			"node_id", "log_time", "metric", "labels_json", "value",
		})
	}
}

/*
Aptos exposes its metrics in the Prometheus text format at: http://localhost:<inspection_port>/metrics
A response looks like:
# HELP aptos_consensus_timeout_count Count the number of timeouts
# TYPE aptos_consensus_timeout_count counter
aptos_consensus_timeout_count 3
aptos_consensus_current_round_voted_power{peer_id="0xabc",hash_index="1"} 1

This is a parser method for metric lines that skips comments and extracts the metric name, labels, and numeric value.

E.g. metric: aptos_consensus_current_round_voted_power{peer_id="0xabc",hash_index="1"} 1
metric = aptos_consensus_current_round_voted_power
labels = {"peer_id": "0xabc", "hash_index": "1"}
value = 1
*/
func parsePrometheusLine(line string) (string, map[string]string, float64, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil, 0, false
	}

	nameAndLabels, valueText, ok := splitPrometheusSample(line) // handle labels with spaces
	if !ok {
		return "", nil, 0, false
	}

	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil {
		return "", nil, 0, false
	}

	labels := map[string]string{}
	metric := nameAndLabels

	if idx := strings.Index(nameAndLabels, "{"); idx >= 0 {
		if !strings.HasSuffix(nameAndLabels, "}") {
			return "", nil, 0, false
		}
		metric = nameAndLabels[:idx]
		labelsText := nameAndLabels[idx+1 : len(nameAndLabels)-1]
		labels = parseLabels(labelsText)
	}

	return metric, labels, value, true
}

func splitPrometheusSample(line string) (string, string, bool) {
	inQuotes := false
	escaped := false
	split := -1

	for i, ch := range line {
		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && inQuotes {
			escaped = true
			continue
		}

		if ch == '"' {
			inQuotes = !inQuotes
			continue
		}

		if !inQuotes && (ch == ' ' || ch == '\t') {
			split = i
			break
		}
	}

	if split < 0 {
		return "", "", false
	}

	left := strings.TrimSpace(line[:split])
	rest := strings.TrimSpace(line[split+1:])
	if left == "" || rest == "" {
		return "", "", false
	}

	valueFields := strings.Fields(rest)
	if len(valueFields) == 0 {
		return "", "", false
	}

	return left, valueFields[0], true
}

func parseLabels(labelsText string) map[string]string {
	labels := make(map[string]string)

	for len(labelsText) > 0 {
		labelsText = strings.TrimSpace(labelsText)
		if labelsText == "" {
			break
		}

		eq := strings.Index(labelsText, "=")
		if eq < 0 {
			break
		}

		key := strings.TrimSpace(labelsText[:eq])
		rest := strings.TrimSpace(labelsText[eq+1:])

		if key == "" {
			break
		}

		if !strings.HasPrefix(rest, "\"") {
			comma := strings.Index(rest, ",")
			if comma < 0 {
				labels[key] = strings.TrimSpace(rest)
				break
			}
			labels[key] = strings.TrimSpace(rest[:comma])
			labelsText = rest[comma+1:]
			continue
		}

		var value strings.Builder
		escaped := false
		i := 1

		for ; i < len(rest); i++ {
			ch := rest[i]

			if escaped {
				switch ch {
				case 'n':
					value.WriteByte('\n')
				default:
					value.WriteByte(ch)
				}
				escaped = false
				continue
			}

			if ch == '\\' {
				escaped = true
				continue
			}

			if ch == '"' {
				break
			}

			value.WriteByte(ch)
		}

		labels[key] = value.String()

		if i >= len(rest) {
			break
		}

		rest = strings.TrimSpace(rest[i+1:])
		if strings.HasPrefix(rest, ",") {
			labelsText = rest[1:]
		} else {
			break
		}
	}

	return labels
}
