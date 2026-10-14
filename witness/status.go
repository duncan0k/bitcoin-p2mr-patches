package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The hash of Ark-0's block 1264, the last block of the published snapshot: a node that has it is on Ark-0.
const block1264 = "00000036e6e81d9884217ca9918e38b9683617b41195a6d4ccc4c4696b31e9d4"

// m3Height is where M3 applies, until the node itself says (gettripwireinfo).
const m3Height = 30000

type tripwire struct {
	Outpoint string `json:"outpoint"`
	State    string `json:"state"`
	Label    string `json:"label"`
}

// status is what the page shows: the node's own answers, nothing the program assumes.
type status struct {
	Version   string `json:"version"`
	Now       string `json:"now"` // UTC
	DataDir   string `json:"dataDir"`
	Node      string `json:"node"` // "starting", "running" or "stopped"
	NodeError string `json:"nodeError"`
	Warmup    string `json:"warmup"` // the node's message while it loads
	Blocks    int64  `json:"blocks"`
	Headers   int64  `json:"headers"`
	IBD       bool   `json:"ibd"`
	TipHash   string `json:"tipHash"`
	Peers     int    `json:"peers"`
	Ark0      string `json:"ark0"` // "pending", "ok" or "mismatch"
	H3        int64  `json:"h3"`
	BlockH3   string `json:"blockH3"`
	// What gettripwireinfo gave: "ok", "unknown" (no answer this time) or "absent" (the node has no such call, so
	// it is not M3). The fields after it hold only with "ok", and are of block TripwireTip.
	TripwireInfo        string     `json:"tripwireInfo"`
	TripwireTip         int64      `json:"tripwireTip"`
	TripwireTipHash     string     `json:"tripwireTipHash"`
	Tripwires           []tripwire `json:"tripwires"`
	ECDisabledNextBlock bool       `json:"ecDisabledNextBlock"`
	Subversion          string     `json:"subversion"`
}

// collector asks the node for its status, at most every two seconds whoever asks.
type collector struct {
	node    *node
	dataDir string

	mu         sync.Mutex
	last       status
	at         time.Time
	subversion string
}

func (c *collector) get() status {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at.IsZero() || time.Since(c.at) >= 2*time.Second {
		c.last, c.at = c.collect(), time.Now()
	}
	return c.last
}

func (c *collector) collect() status {
	s := status{Version: version, Now: nowUTC(), DataDir: c.dataDir, Ark0: "pending", H3: m3Height, TripwireInfo: "unknown",
		Tripwires: []tripwire{}}
	s.Node, s.NodeError = c.node.state()
	rpc := c.node.client()
	if rpc == nil || s.Node == "stopped" {
		return s
	}
	var chain struct {
		Blocks  int64  `json:"blocks"`
		Headers int64  `json:"headers"`
		IBD     bool   `json:"initialblockdownload"`
		Best    string `json:"bestblockhash"`
	}
	if err := rpc.call("getblockchaininfo", nil, &chain); err != nil {
		var answer *rpcError
		if errors.As(err, &answer) && answer.Code == rpcInWarmup {
			s.Warmup = answer.Message
		}
		return s
	}
	c.node.markRunning()
	s.Node = "running"
	s.Blocks, s.Headers, s.IBD, s.TipHash = chain.Blocks, chain.Headers, chain.IBD, chain.Best
	_ = rpc.call("getconnectioncount", nil, &s.Peers)
	if s.Blocks >= 1264 {
		var hash string
		if rpc.call("getblockhash", []any{1264}, &hash) == nil {
			if hash == block1264 {
				s.Ark0 = "ok"
			} else {
				s.Ark0 = "mismatch"
			}
		}
	}
	// The tripwire states and the flag come with the block they are of, which may be newer than the tip above.
	var tw struct {
		TipHash    string     `json:"tip_hash"`
		TipHeight  *int64     `json:"tip_height"`
		Height     int64      `json:"height"`
		Tripwires  []tripwire `json:"tripwires"`
		ECDisabled *bool      `json:"ec_disabled_next_block"`
	}
	err := rpc.call("gettripwireinfo", nil, &tw)
	var answer *rpcError
	switch {
	case err == nil && tw.TipHash != "" && tw.TipHeight != nil && tw.Height > 0 && tw.Tripwires != nil && tw.ECDisabled != nil:
		s.TripwireInfo, s.H3 = "ok", tw.Height
		s.TripwireTip, s.TripwireTipHash = *tw.TipHeight, tw.TipHash
		s.Tripwires, s.ECDisabledNextBlock = tw.Tripwires, *tw.ECDisabled
	case errors.As(err, &answer) && answer.Code == rpcMethodNotFound:
		s.TripwireInfo = "absent"
	}
	if s.Blocks >= s.H3 {
		_ = rpc.call("getblockhash", []any{s.H3}, &s.BlockH3)
	}
	if c.subversion == "" {
		var network struct {
			Subversion string `json:"subversion"`
		}
		if rpc.call("getnetworkinfo", nil, &network) == nil {
			c.subversion = network.Subversion
		}
	}
	s.Subversion = c.subversion
	return s
}

func nowUTC() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05")
}

// witnessReport is the text a witness shares: what the node reported and the last transaction it judged. It
// holds no address of the computer and no path.
func witnessReport(s status, last *checkResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ark-0 witness report (Ark0Witness %s)\n", s.Version)
	fmt.Fprintf(&b, "Time (UTC): %s\n", s.Now)
	if s.Node != "running" {
		fmt.Fprintf(&b, "Node: %s\n", s.Node)
		return b.String()
	}
	if s.Subversion != "" {
		fmt.Fprintf(&b, "Node software: %s\n", s.Subversion)
	}
	switch s.Ark0 {
	case "ok":
		b.WriteString("Block 1264 matches Ark-0: yes\n")
	case "mismatch":
		b.WriteString("Block 1264 matches Ark-0: NO\n")
	default:
		b.WriteString("Block 1264 matches Ark-0: not checked yet\n")
	}
	// With the tripwires known, the tip is the block their states are of.
	if s.TripwireInfo == "ok" {
		fmt.Fprintf(&b, "Tip: height %d, hash %s\n", s.TripwireTip, s.TripwireTipHash)
	} else {
		fmt.Fprintf(&b, "Tip: height %d, hash %s\n", s.Blocks, s.TipHash)
	}
	if s.BlockH3 != "" {
		fmt.Fprintf(&b, "Block %d (M3): %s\n", s.H3, s.BlockH3)
	} else {
		fmt.Fprintf(&b, "Block %d (M3): not reached yet\n", s.H3)
	}
	switch s.TripwireInfo {
	case "ok":
		for _, w := range s.Tripwires {
			fmt.Fprintf(&b, "Tripwire %s %s: %s\n", w.Label, w.Outpoint, w.State)
		}
		fmt.Fprintf(&b, "ec_disabled_next_block: %t\n", s.ECDisabledNextBlock)
	case "absent":
		b.WriteString("Tripwires: unknown, this node has no gettripwireinfo (not the M3 release)\n")
	default:
		b.WriteString("Tripwires: unknown, the node did not answer gettripwireinfo just now\n")
	}
	switch {
	case last == nil:
	case last.Error != "":
		fmt.Fprintf(&b, "Checked a transaction at %s UTC: the node could not read it: %s\n", last.At, last.Error)
	case last.Allowed:
		fmt.Fprintf(&b, "Checked transaction %s at %s UTC: allowed\n", last.Txid, last.At)
	default:
		fmt.Fprintf(&b, "Checked transaction %s at %s UTC: rejected, %s\n", last.Txid, last.At, rejection(*last))
	}
	return b.String()
}
