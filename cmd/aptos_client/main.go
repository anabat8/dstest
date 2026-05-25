/*
This binary is invoked by dstest client scripts
(see aptos/aptos_client.sh).

Each invocation submits a batch of transactions to a single Aptos validator
REST endpoint and exits.

Client workers use pre-generated Aptos accounts stored under:

    ${BASE_DIR}/genesis/clients/cNN.yaml

Each client account is funded from the genesis root account at the start of each test iter.

Each account has an independent sequence-number stream; combined with the
ByzzFuzzScheduler's 1-token mutex this guarantees no two
workers share a client sender, eliminating sequence-number races.

Transactions are submitted through validator REST APIs, but account state is
global blockchain state replicated across all validators through consensus.

Batch size, target validator port, and client identity are controlled via
CLI flags so they can vary between experiments.

No faucet or runtime account minting is required.
*/

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aptos-labs/aptos-go-sdk"
	"github.com/aptos-labs/aptos-go-sdk/api"
	"github.com/aptos-labs/aptos-go-sdk/crypto"
	"github.com/google/uuid"

	apt "github.com/egeberkaygulcan/dstest/cmd/dstest/network/aptos"
)

/*
One (client, sender) pair targeting a single validator REST endpoint.
The sender account is used to sign the transactions built by the client,
and which the node at the target port has to gossip to all other peers.
*/
type submitter struct {
	port   int
	client *aptos.Client
	sender *aptos.Account
}

/*
Checks if the node at the target port has reached the specified block height.
If yes, then the client submitter can proceed to send transactions, otherwise
it has to retry in the future (the binary exits with code 2, a signal which the
scheduler interprets as a retry later, don't drop the script).
*/
func (s *submitter) HasHeight(blockHeight uint64) bool {
	nodeInfo, err := s.client.Info()
	if err != nil {
		fmt.Printf("[Port: %d] Error fetching node info: %s\n", s.port, err)
		return false
	}

	latestBlockHeight := nodeInfo.BlockHeight()
	if latestBlockHeight >= blockHeight {
		return true
	}
	return false
}

/*
Builds a (client, sender) pair: one REST client bound to a validator node URL
plus an Aptos account used to sign transactions submitted through that client.

The signing identity is loaded from a generated client-account YAML containing:
  - account_private_key
  - account_address
*/
func newSubmitter(port int, identityPath string) (*submitter, error) {
	cfg := aptos.NetworkConfig{
		NodeUrl: fmt.Sprintf("http://localhost:%d/v1", port),
		ChainId: 42,
	}
	client, err := aptos.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("aptos.NewClient: %w", err)
	}

	privKeyHex, addrHex, err := apt.LoadIdentity(identityPath)
	if err != nil {
		return nil, err
	}

	keyBytes, err := crypto.ParsePrivateKey(privKeyHex, crypto.PrivateKeyVariantEd25519, false)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	privKey := &crypto.Ed25519PrivateKey{}
	if err := privKey.FromBytes(keyBytes); err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}

	var addr aptos.AccountAddress
	if err := addr.ParseStringRelaxed(addrHex); err != nil {
		return nil, fmt.Errorf("parse sender address: %w", err)
	}

	account, err := aptos.NewAccountFromSigner(privKey, addr)
	if err != nil {
		return nil, fmt.Errorf("aptos.NewAccountFromSigner: %w", err)
	}
	return &submitter{port: port, client: client, sender: account}, nil
}

/*
Builds a simple coin-transfer payload to a hardcoded receiver account (0xBEEF).
*/
func transferPayload() aptos.TransactionPayload {
	var receiver aptos.AccountAddress
	if err := receiver.ParseStringRelaxed("0xBEEF"); err != nil {
		panic("parse receiver: " + err.Error())
	}
	p, err := aptos.CoinTransferPayload(nil, receiver, 1)
	if err != nil {
		panic("CoinTransferPayload: " + err.Error())
	}
	return aptos.TransactionPayload{Payload: p}
}

/*
Submits numTransactions concurrently using one submitter
(one validator REST endpoint), then waits until the submission validator
reports each tx as committed (i.e., included in a committed block and
executed against that validator's local state).

This is the submission validator's view, not a global one: other validators
may still be lagging. Cluster-wide agreement is checked separately by the aptos
agreement monitor.

Each dstest client worker uses an independent Aptos account identity;
the ByzzFuzz scheduler ensures another workes isn't using it concurrently.

If the waiting for commits times out, this could indicate a potential
liveness signal (txs were accepted but the submission validator did not
see them committed within the configured timeout window) or there was an
error in the tx submission flow.
*/
func sendBatch(s *submitter, numTransactions uint64) error {
	payload := transferPayload()
	payloads := make(chan aptos.TransactionBuildPayload, 50)
	results := make(chan aptos.TransactionSubmissionResponse, 50)
	go s.client.BuildSignAndSubmitTransactions(s.sender, payloads, results)

	go func() {
		rootId := uint64(uuid.New().ID())
		for i := uint64(0); i < numTransactions; i++ {
			payloads <- aptos.TransactionBuildPayload{
				Id:    rootId + i,
				Type:  aptos.TransactionSubmissionTypeSingle,
				Inner: payload,
			}
		}
		close(payloads)
	}()

	hashes := make([]string, 0, numTransactions)
	for result := range results {
		if result.Err != nil {
			return fmt.Errorf("tx %d: %w", result.Id, result.Err)
		}
		if result.Response != nil {
			hashes = append(hashes, string(result.Response.Hash))
		}
	}

	if len(hashes) == 0 {
		return nil
	}

	err := s.client.PollForTransactions(
		hashes,
		aptos.PollPeriod(500*time.Millisecond),
		aptos.PollTimeout(30*time.Second),
	)
	if err != nil {
		return fmt.Errorf("wait for commit: %w", err)
	}

	// fetch each committed tx and log its on-chain location
	for _, h := range hashes {
		tx, err := s.client.TransactionByHash(h)
		if err == nil && tx != nil {
			if u, ok := tx.Inner.(*api.UserTransaction); ok {
				fmt.Fprintf(os.Stdout,
					"[aptos_client] committed tx=%s version=%d success=%v\n acc_root=%s\n",
					h, u.Version, u.Success, u.AccumulatorRootHash)
			}
		}
	}

	return nil
}

func main() {
	portArg := flag.Int("port", 8000,
		"Validator REST API port (e.g. 8000, 8010, 8020, 8030).")
	identityArg := flag.String("identity",
		"/tmp/aptos-dstest/genesis/clients/c00.yaml",
		"Path to a client identity YAML containing account_private_key and account_address.")
	numTxs := flag.Uint64("num-txs", 10, "Number of transactions to submit in this batch.")
	blockHeight := flag.Uint64("block-height", 0, "Height of the blockchain at which to send the batch of txs to the node.")
	flag.Parse()

	s, err := newSubmitter(*portArg, *identityArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup failed for port %d: %v\n", *portArg, err)
		os.Exit(1)
	}

	if !s.HasHeight(*blockHeight) {
		fmt.Fprintf(os.Stderr, "node at port %d has not reached block height %d yet\n", s.port, *blockHeight)
		os.Exit(2)
	}

	fmt.Fprintf(os.Stderr, "[aptos_client] sending %d txs to localhost:%d (sender=%s)\n",
		*numTxs, s.port, s.sender.Address.String())
	start := time.Now()
	if err := sendBatch(s, *numTxs); err != nil {
		fmt.Fprintf(os.Stderr, "sendBatch failed on port %d: %v\n", s.port, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[aptos_client] port %d done in %s\n", s.port, time.Since(start))
}
