package main

import (
	"bytes"
	cryptoRand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/crypto/nacl/box"
)

var (
	errExpectedEmptyKeys = errors.New("expected encrypted keys to be unset")
)

// leaderSync holds the state and code that we need for a one-off sync with a
// worker enclave.
type leaderSync struct {
	attester
	keys *enclaveKeys
}

// asLeader returns a new leaderSync struct.
func asLeader(keys *enclaveKeys, a attester) *leaderSync {
	return &leaderSync{
		attester: a,
		keys:     keys,
	}
}

func retryWithBackoff(maxRetries int, maxWait time.Duration, operation func() (*http.Response, error)) (*http.Response, error) {
	var resp *http.Response
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = operation()
		if err == nil && resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		waitTime := time.Duration(math.Pow(2, float64(attempt))) * time.Second
		if waitTime > maxWait {
			waitTime = maxWait
		}

		elog.Printf("Attempt %d failed, retrying in %v: %v", attempt+1, waitTime, err)
		time.Sleep(waitTime)
	}
	return resp, err // Return the last response and error
}

// syncWith makes the leader initiate key synchronization with the given worker
// enclave.
func (s *leaderSync) syncWith(worker *url.URL) (err error) {
	var (
		reqBody   attstnBody
		encrypted []byte
	)
	defer func() {
		if err == nil {
			elog.Printf("Successfully synced with worker %s.", worker.Host)
		} else {
			elog.Printf("Error syncing with worker %s: %v", worker.Host, err)
		}
	}()

	// Step 1: Create a nonce that the worker must embed in its attestation
	// document, to prevent replay attacks.
	nonce, err := newNonce()
	if err != nil {
		return err
	}

	elog.Printf("Created nonce: %v", nonce)

	// Step 2: Request the worker's attestation document, and provide the
	// previously-generated nonce.
	reqURL := *worker
	reqURL.RawQuery = fmt.Sprintf("nonce=%x", nonce)
	resp, err := retryWithBackoff(5, 1*time.Minute, func() (*http.Response, error) {
		return newUnauthenticatedHTTPClient().Get(reqURL.String())
	})
	if err != nil {
		return fmt.Errorf("failed to get attestation document: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return errNo200(resp.StatusCode, reqURL.String(), "while requesting attestation")
	}

	// Step 3: Verify the worker's attestation document and extract its
	// auxiliary information.
	maxReadLen := base64.StdEncoding.EncodedLen(maxAttstnBodyLen)
	jsonBody, err := io.ReadAll(newLimitReader(resp.Body, maxReadLen))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := json.Unmarshal(jsonBody, &reqBody); err != nil {
		return err
	}
	if len(reqBody.EncryptedKeys) != 0 {
		return errExpectedEmptyKeys
	}
	attstnDoc, err := base64.StdEncoding.DecodeString(reqBody.Document)
	if err != nil {
		return err
	}
	aux, err := s.verifyAttstn(attstnDoc, nonce)
	if err != nil {
		return err
	}
	workerAux := aux.(*workerAuxInfo)

	// Step 4: Encrypt the leader's enclave keys with the ephemeral public key
	// that the worker put into its auxiliary information.
	pubKey := &[boxKeyLen]byte{}
	copy(pubKey[:], workerAux.PublicKey[:])
	jsonKeys, err := json.Marshal(s.keys.copy())
	if err != nil {
		return err
	}
	encrypted, err = box.SealAnonymous(nil, jsonKeys, pubKey, cryptoRand.Reader)
	if err != nil {
		return err
	}

	// Step 5: Create the leader's auxiliary information, consisting of the
	// worker's nonce and a hash of the encrypted enclave keys.
	hash := sha256.Sum256(encrypted)
	leaderAux := &leaderAuxInfo{
		WorkersNonce:    workerAux.WorkersNonce,
		HashOfEncrypted: hash[:],
	}
	attstnDoc, err = s.createAttstn(leaderAux)
	if err != nil {
		return err
	}

	// Step 6: Send the leader's attestation document to the worker.
	jsonBody, err = json.Marshal(&attstnBody{
		Document:      base64.StdEncoding.EncodeToString(attstnDoc),
		EncryptedKeys: base64.StdEncoding.EncodeToString(encrypted),
	})
	if err != nil {
		return err
	}
	resp, err = newUnauthenticatedHTTPClient().Post(
		worker.String(),
		"text/plain",
		bytes.NewReader(jsonBody),
	)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return errNo200(resp.StatusCode, worker.String(), "while syncing keys")
	}

	return nil
}
