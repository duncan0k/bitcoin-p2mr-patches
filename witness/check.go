package main

import (
	"errors"
	"strings"
)

// ecDisabledMessage is how the node words the drill's rule when it refuses an elliptic curve signature in a
// leaf of version 0xe0 once a tripwire has been spent.
const ecDisabledMessage = "Elliptic curve signature checks are disabled in this leaf"

// maxTxHex bounds a pasted transaction: half a megabyte, more than any standard transaction.
const maxTxHex = 1 << 20

// checkResult is the node's verdict on a pasted transaction.
type checkResult struct {
	At         string `json:"at"` // UTC
	Txid       string `json:"txid"`
	Allowed    bool   `json:"allowed"`
	Reason     string `json:"reason"`
	Details    string `json:"details"`
	ECDisabled bool   `json:"ecDisabled"` // refused under the drill's rule
	Error      string `json:"error"`      // the node could not read the transaction
}

// cleanHex drops the spaces and line breaks a paste may carry and accepts only hexadecimal of whole bytes.
func cleanHex(in string) (string, bool) {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			continue
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			b.WriteRune(r)
		default:
			return "", false
		}
		if b.Len() > maxTxHex {
			return "", false
		}
	}
	s := b.String()
	return s, s != "" && len(s)%2 == 0
}

// checkTransaction asks the node whether it would accept the transaction. testmempoolaccept only judges it
// against the node's rules: nothing is broadcast, and this program has no call that would.
func checkTransaction(rpc *rpcClient, raw string) (checkResult, error) {
	res := checkResult{At: nowUTC()}
	var answers []struct {
		Txid    string `json:"txid"`
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reject-reason"`
		Details string `json:"reject-details"`
	}
	// A maximum fee rate of 0 accepts any fee rate, so that only the rules decide.
	err := rpc.call("testmempoolaccept", []any{[]string{raw}, 0}, &answers)
	var answer *rpcError
	switch {
	case errors.As(err, &answer):
		res.Error = clip(answer.Message, 300)
		return res, nil
	case err != nil:
		return res, err
	case len(answers) != 1:
		return res, errors.New("unexpected answer from the node")
	}
	a := answers[0]
	res.Txid, res.Allowed, res.Reason, res.Details = a.Txid, a.Allowed, clip(a.Reason, 300), clip(a.Details, 300)
	res.ECDisabled = !a.Allowed && strings.Contains(a.Reason+" "+a.Details, ecDisabledMessage)
	return res, nil
}

// rejection is the node's reason, with its details when they add something.
func rejection(r checkResult) string {
	if r.Details != "" && r.Details != r.Reason {
		return r.Reason + " (" + r.Details + ")"
	}
	return r.Reason
}
