package aptos

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/aptos-labs/aptos-go-sdk"
	"github.com/aptos-labs/aptos-go-sdk/api"
)

// Monitor for checking consensus guarantees (agreement and liveness) among aptos replicas during test runs
type AgreementMonitor struct {
	finishSignal chan struct{}
	contentChan  chan Content

	blockCommitsLog  *csv.Writer
	blockCommitsFile *os.File
	agreementFile    *os.File
	log              bufio.Writer

	livenessTimeout int
}

type Content struct {
	NodeId  int
	Epoch   uint64
	Round   uint64
	Block   api.Block
	AccHash string
	LedgerV string
}

func (c Content) Equals(other Content) bool {
	return c.Epoch == other.Epoch &&
		c.Round == other.Round &&
		c.Block.BlockHeight == other.Block.BlockHeight &&
		c.AccHash == other.AccHash &&
		c.Block.BlockHash == other.Block.BlockHash &&
		c.LedgerV == other.LedgerV
}

/*
The agreement monitor periodically polls each node for its latest block and logs the results.
It also compares the latest blocks across nodes to detect any disagreements in ledger state or any liveness violations.
*/
func StartAgreementMonitor(
	outputDir, testName, schedulerType string,
	iteration int,
	numReplicas, basePort int,
	livenessTimeout int,
) (*AgreementMonitor, error) {
	quit := make(chan struct{})
	contentChan := make(chan Content, 10)
	monitor := &AgreementMonitor{finishSignal: quit, contentChan: contentChan, livenessTimeout: livenessTimeout}
	monitor.Init(outputDir, testName, schedulerType, iteration)

	go monitor.Monitor()
	for i := 0; i < numReplicas; i++ {
		go monitor.PollForNode(i, basePort+i*10)
	}
	return monitor, nil
}

/*
PollForNode continuously polls the given node (once per second) for its latest block info and sends it to the monitor.
*/
func (m *AgreementMonitor) PollForNode(node_id, port int) {
	client, err := aptos.NewClient(aptos.NetworkConfig{NodeUrl: fmt.Sprintf("http://localhost:%d/v1/", port), ChainId: 42})
	if err != nil {
		return
	}

	for {
		select {
		case <-m.finishSignal:
			return
		default:
			nodeInfo, err := client.Info()
			if err != nil {
				fmt.Printf("[Port: %d] Error fetching node info: %s\n", port, err)
				continue
			}

			latestBlockHeight := nodeInfo.BlockHeight()
			epoch := nodeInfo.Epoch()
			ledgerV := nodeInfo.LedgerVersionStr

			block, err := client.BlockByHeight(latestBlockHeight, false)
			if err != nil {
				fmt.Printf("[Port: %d] Error fetching block info: %s\n", port, err)
				continue
			}

			lastVersion := block.LastVersion
			commitedTx, err := client.TransactionByVersion(lastVersion)
			if err != nil {
				fmt.Printf("[Port: %d] Error fetching transaction info: %s\n", port, err)
				continue
			}

			var accHash string
			switch t := commitedTx.Inner.(type) {
			case *api.UserTransaction:
				accHash = t.AccumulatorRootHash
			case *api.BlockMetadataTransaction:
				accHash = t.AccumulatorRootHash
			case *api.StateCheckpointTransaction:
				accHash = t.AccumulatorRootHash
			case *api.BlockEpilogueTransaction:
				accHash = t.AccumulatorRootHash
			case *api.GenesisTransaction:
				accHash = t.AccumulatorRootHash
			case *api.ValidatorTransaction:
				accHash = t.AccumulatorRootHash
				// *api.PendingTransaction has no info; shouldn't happen for a committed version
			}

			var round uint64
			firstTx, err := client.TransactionByVersion(block.FirstVersion)
			if err != nil {
				fmt.Printf("[Port: %d] Error fetching round info: %s\n", port, err)
			} else {
				if bmt, ok := firstTx.Inner.(*api.BlockMetadataTransaction); ok {
					round = bmt.Round
				}
			}

			m.Put(Content{
				NodeId:  node_id,
				Epoch:   epoch,
				Round:   round,
				Block:   *block,
				AccHash: accHash,
				LedgerV: ledgerV,
			})

			time.Sleep(time.Millisecond * 1000)
		}
	}
}

/*
The monitor continuously receives block commits from the pollers and checks for agreement/liveness across nodes.
We either receive a signal that the test iteration has ended, info on a block that has been committed,
or that T(=30 by default) seconds have passed without a new block commit for a new blockchain height (indicating
that a quorum of nodes have failed to reach consensus for new block in order to advance the height, hence a liveness violation).
In case of receiving info on a block commit, we log the block info and check if it matches the other block info logged
from other nodes at the same height, hence detecting any disagreement in ledger state across nodes.
*/
func (m *AgreementMonitor) Monitor() {
	byHeight := make(map[uint64][]Content)
	livenessDuration := time.Second * time.Duration(m.livenessTimeout)
	timer := time.NewTimer(livenessDuration)
	maxHeight := uint64(0)

	for {
		select {
		case <-m.finishSignal:
			return
		case content := <-m.contentChan:
			if content.Block.BlockHeight > maxHeight {
				maxHeight = content.Block.BlockHeight
				timer.Reset(livenessDuration)
			}

			m.blockCommitsLog.Write([]string{
				fmt.Sprintf("%d", content.NodeId),
				fmt.Sprintf("%d", content.Block.BlockHeight),
				fmt.Sprintf("%d", content.Epoch),
				fmt.Sprintf("%d", content.Round),
				content.Block.BlockHash,
				content.AccHash,
				content.LedgerV,
				fmt.Sprintf("%d", content.Block.BlockTimestamp),
				fmt.Sprintf("%d", time.Now().UnixMicro()),
			})
			m.blockCommitsLog.Flush()

			for _, other := range byHeight[content.Block.BlockHeight] {
				if !content.Equals(other) {
					m.log.WriteString(
						fmt.Sprintf("Disagreement detected at height %d between node %d and node %d\n",
							content.Block.BlockHeight,
							content.NodeId,
							other.NodeId),
					)
					m.log.Flush()
				}
			}
			byHeight[content.Block.BlockHeight] = append(byHeight[content.Block.BlockHeight], content)
		case <-timer.C:
			m.log.WriteString(fmt.Sprintf(
				"Liveness failure: no new blocks committed in %d seconds\n",
				m.livenessTimeout))
			m.log.Flush()
		}
	}
}

/*
Stop signals the poller to shut down cleanly and waits for it to exit
*/
func (m *AgreementMonitor) Stop() {
	close(m.finishSignal)
	m.closeAndFlushFiles()
}

func (m *AgreementMonitor) Put(content Content) {
	m.contentChan <- content
}

func (m *AgreementMonitor) closeAndFlushFiles() {
	if m.blockCommitsFile != nil {
		m.blockCommitsLog.Flush()
		m.blockCommitsFile.Close()
	}

	if m.agreementFile != nil {
		m.log.Flush()
		m.agreementFile.Close()
	}
}

/*
Init initializes the agreement monitor by creating the necessary log files for the given test iteration.
For each iteration of the experiment, we create a csv with block commits for each replica and
a log file for agreement/liveness issues.
*/
func (m *AgreementMonitor) Init(
	outputDir, testName, schedulerType string,
	iteration int,
) {
	m.closeAndFlushFiles()

	path := filepath.Join(outputDir,
		fmt.Sprintf("%s_%s_%d",
			testName,
			schedulerType,
			iteration),
		"blockCommits.csv")

	os.MkdirAll(filepath.Dir(path), os.ModePerm)

	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("Failed to create block commits CSV %v", err)
	} else {
		m.blockCommitsFile = f
		m.blockCommitsLog = csv.NewWriter(f)
		m.blockCommitsLog.Write([]string{
			"node_id", "height", "epoch", "round", "block_hash", "accumulator_root_hash", "version",
			"timestamp_usecs", "log_time",
		})
	}

	path = filepath.Join(outputDir,
		fmt.Sprintf("%s_%s_%d",
			testName,
			schedulerType,
			iteration),
		"agreement.log")

	f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("error opening agreement log file: %v", err)
	}

	m.agreementFile = f
	m.log = *bufio.NewWriter(f)
}
