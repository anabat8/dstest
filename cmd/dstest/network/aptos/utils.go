package aptos

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aptos-labs/aptos-go-sdk"
	"github.com/aptos-labs/aptos-go-sdk/crypto"
)

/*
CoreResourcesAddress is the genesis-funded mint authority. Root's private key
authorizes it (the SDK-derived alias in root/private-keys.yaml has 0 balance).
*/
const CoreResourcesAddress = "0xa550c18"

/*
Reads account_private_key and account_address from a genesis identity YAML.
Works for per-validator validator-identity.yaml, genesis
root/private-keys.yaml, or client identities in genesis/clients/c0*.yaml.
*/
func LoadIdentity(path string) (privKeyHex, addrHex string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if v, ok := strings.CutPrefix(line, "account_private_key:"); ok {
			privKeyHex = strings.Trim(strings.TrimSpace(v), `"`)
		} else if v, ok := strings.CutPrefix(line, "account_address:"); ok {
			addrHex = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("scan %s: %w", path, err)
	}
	if privKeyHex == "" || addrHex == "" {
		return "", "", fmt.Errorf("missing account_private_key or account_address in %s", path)
	}
	return privKeyHex, addrHex, nil
}

/*
LoadRoot reads ${baseDir}/genesis/root/private-keys.yaml, returns an SDK
*Account that signs with the root key but uses 0xA550C18 as the on-chain
sender (the SDK-derived alias in the YAML has 0 balance; the funded supply
lives at core_resources).
*/
func LoadRoot(baseDir string) (*aptos.Account, error) {
	path := filepath.Join(baseDir, "genesis", "root", "private-keys.yaml")
	privKeyHex, _, err := LoadIdentity(path)
	if err != nil {
		return nil, err
	}

	keyBytes, err := crypto.ParsePrivateKey(privKeyHex, crypto.PrivateKeyVariantEd25519, false)
	if err != nil {
		return nil, fmt.Errorf("parse root private key: %w", err)
	}
	privKey := &crypto.Ed25519PrivateKey{}
	if err := privKey.FromBytes(keyBytes); err != nil {
		return nil, fmt.Errorf("decode root private key: %w", err)
	}

	var addr aptos.AccountAddress
	if err := addr.ParseStringRelaxed(CoreResourcesAddress); err != nil {
		return nil, fmt.Errorf("parse core_resources address: %w", err)
	}

	acc, err := aptos.NewAccountFromSigner(privKey, addr)
	if err != nil {
		return nil, fmt.Errorf("aptos.NewAccountFromSigner: %w", err)
	}
	return acc, nil
}

/*
LoadClientAddresses reads ${baseDir}/genesis/clients/c*.yaml and returns the
account_address of each one (the funding targets) in the same order as the files.
*/
func LoadClientAddresses(baseDir string) ([]aptos.AccountAddress, error) {
	pattern := filepath.Join(baseDir, "genesis", "clients", "c*.yaml")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", pattern, err)
	}

	addrs := make([]aptos.AccountAddress, 0, len(files))
	for _, f := range files {
		_, addrHex, err := LoadIdentity(f)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", f, err)
		}
		var addr aptos.AccountAddress
		if err := addr.ParseStringRelaxed(addrHex); err != nil {
			return nil, fmt.Errorf("parse address in %s: %w", f, err)
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}
