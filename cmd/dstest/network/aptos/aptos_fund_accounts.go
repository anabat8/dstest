package aptos

/*
Fund accounts from root account
Note: we are using the REST endpoint of validator node 0 for initial
account funding; transactions propagate to the rest of the validator set
through Aptos mempool gossip.

Example:
	- all validators will know that client account 0x123 has balance
	- then a later request:
		client -> validator 2
		(submit tx from 0x123 through localhost:BASEPORT+i*10/v1) works
		since validator 2 knows that the account exists and has balance

After submission, we poll every validator until each has executed the fund txs
in its local state.
*/
import (
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/aptos-labs/aptos-go-sdk"
)

type FundingTask struct {
	done    chan struct{}
	err     error
	stopped atomic.Bool
}

func (t *FundingTask) IsDone() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

// Wait blocks until account funding finishes and returns its result.
func (t *FundingTask) Wait() error {
	<-t.done
	return t.err
}

/*
Stop marks the funding task as canceled (iteration ended).
*/
func (t *FundingTask) Stop() {
	t.stopped.Store(true)
}

/*
Start a background task to fund client accounts from the root account.
The aptos client accounts are considered to be funded successfully when
all validators have received the funding transactions and committed them.
*/
func StartFundingClientAccounts(baseReplicaPort int, baseDir string, numValidators int, logger *log.Logger) *FundingTask {
	t := &FundingTask{done: make(chan struct{})}
	go func() {
		defer close(t.done)
		logger.Printf("Waiting for Aptos validator REST endpoints before funding client accounts\n")
		t.err = waitForValidatorRestEndpoints(baseReplicaPort, numValidators, 120*time.Second)
		if t.err == nil {
			t.err = fundClientAccounts(baseReplicaPort, baseDir, numValidators)
		}
		if t.stopped.Load() {
			return
		}
		if t.err != nil {
			logger.Printf("Aptos client funding failed: %s\n", t.err)
			return
		}
		logger.Printf("Aptos client accounts funded successfully\n")
	}()
	return t
}

const validatorRESTProbeInterval = 500 * time.Millisecond

// Concurrent local networks can expose REST before every validator has caught up.
const aptosFundingCommitTimeout = 180 * time.Second

func waitForValidatorRestEndpoints(baseReplicaPort int, numValidators int, timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)

	for {
		pendingPorts := make([]int, 0, numValidators)
		for nodeIdx := 0; nodeIdx < numValidators; nodeIdx++ {
			port := baseReplicaPort + nodeIdx*10
			response, err := client.Get(fmt.Sprintf("http://localhost:%d/v1", port))
			ready := false
			if err == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				ready = response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
			}
			if !ready {
				pendingPorts = append(pendingPorts, port)
			}
		}

		if len(pendingPorts) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("validator REST endpoints not ready within %s; pending ports: %v", timeout, pendingPorts)
		}
		time.Sleep(min(validatorRESTProbeInterval, time.Until(deadline)))
	}
}

const AmountPerAccount = 1000000000 // 10 APT

/*
Submits one funding tx per client account in a single batch, then waits for
all of them to be committed by each validator by calling PollForTransactions.
*/
func fundClientAccounts(baseReplicaPort int, baseDir string, numValidators int) error {
	cfg := aptos.NetworkConfig{
		NodeUrl: fmt.Sprintf("http://localhost:%d/v1", baseReplicaPort),
		ChainId: 42,
	}

	client, err := aptos.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("aptos.NewClient: %w", err)
	}

	rootAccSender, err := LoadRoot(baseDir)
	if err != nil {
		return fmt.Errorf("load root: %w", err)
	}

	clientAddrReceiver, err := LoadClientAddresses(baseDir)
	if err != nil {
		return fmt.Errorf("load client addresses: %w", err)
	}

	payloads := make([]aptos.TransactionPayload, 0, len(clientAddrReceiver))
	for _, receiver := range clientAddrReceiver {
		p, err := aptos.CoinTransferPayload(nil, receiver, AmountPerAccount)
		if err != nil {
			return fmt.Errorf("CoinTransferPayload %s: %w", receiver.String(), err)
		}
		payloads = append(payloads, aptos.TransactionPayload{Payload: p})
	}

	payloadsChan := make(chan aptos.TransactionBuildPayload, len(payloads))
	results := make(chan aptos.TransactionSubmissionResponse, len(payloads))
	go client.BuildSignAndSubmitTransactions(rootAccSender, payloadsChan, results)

	go func() {
		for i, p := range payloads {
			payloadsChan <- aptos.TransactionBuildPayload{
				Id:    uint64(i),
				Type:  aptos.TransactionSubmissionTypeSingle,
				Inner: p,
			}
		}
		close(payloadsChan)
	}()

	hashes := make([]string, 0, len(payloads))
	for r := range results {
		if r.Err != nil {
			return fmt.Errorf("submit fund tx %d: %w", r.Id, r.Err)
		}
		if r.Response != nil {
			hashes = append(hashes, string(r.Response.Hash))
		}
	}

	if len(hashes) == 0 {
		return nil
	}

	// Wait until every validator has the funding txs committed and
	// executed in its local state
	for nodeIdx := 0; nodeIdx < numValidators; nodeIdx++ {
		nc, _ := aptos.NewClient(aptos.NetworkConfig{
			NodeUrl: fmt.Sprintf("http://localhost:%d/v1", baseReplicaPort+nodeIdx*10),
			ChainId: 42,
		})
		if err := nc.PollForTransactions(
			hashes,
			aptos.PollPeriod(500*time.Millisecond),
			aptos.PollTimeout(aptosFundingCommitTimeout),
		); err != nil {
			return fmt.Errorf("wait for fund commits on node %d: %w", nodeIdx, err)
		}
	}

	return nil
}
