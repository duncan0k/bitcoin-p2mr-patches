package main

import (
	"encoding/json"
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

// The lattice tripwires of M4-T: gettripwireinfo has a "lattice" object where these rules have a height. Without
// it, the page and the report say nothing about M4-T.
const (
	latticeNone    = ""        // the node gave no lattice object
	latticeOK      = "ok"      // the fields below hold
	latticeUnknown = "unknown" // no answer this time, or a malformed object, from a node that gave one before
)

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
	Node      string `json:"node"` // "starting", "running", "noanswer" (running, but no answer this time) or "stopped"
	NodeError string `json:"nodeError"`
	Warmup    string `json:"warmup"` // the node's message while it loads
	Blocks    int64  `json:"blocks"`
	Headers   int64  `json:"headers"`
	IBD       bool   `json:"ibd"`
	TipHash   string `json:"tipHash"`
	Peers     int    `json:"peers"`
	Ark0      string `json:"ark0"` // "pending", "ok" or "mismatch"
	H3        int64  `json:"h3"`
	ReachedH3 bool   `json:"reachedH3"` // the newest tip the node reported is at or past H3
	BlockH3   string `json:"blockH3"`   // the hash of block H3, once read
	// What gettripwireinfo gave: "ok", "unknown" (no answer this time) or "absent" (the node has no such call, so
	// it is not M3). The fields after it hold only with "ok", and are of block TripwireTip.
	TripwireInfo        string     `json:"tripwireInfo"`
	TripwireTip         int64      `json:"tripwireTip"`
	TripwireTipHash     string     `json:"tripwireTipHash"`
	Tripwires           []tripwire `json:"tripwires"`
	ECDisabledNextBlock bool       `json:"ecDisabledNextBlock"`
	// M4-T: what the "lattice" object of gettripwireinfo gave, latticeNone, latticeOK or latticeUnknown. The
	// fields after it hold only with latticeOK, and are of block TripwireTip.
	LatticeInfo            string     `json:"latticeInfo"`
	H4T                    int64      `json:"h4t"`
	ReachedH4T             bool       `json:"reachedH4T"` // the newest tip the node reported is at or past H4T
	LatticeTripwires       []tripwire `json:"latticeTripwires"`
	MLDSADisabledNextBlock bool       `json:"mldsaDisabledNextBlock"`
	Subversion             string     `json:"subversion"`
}

// collector asks the node for its status, at most every two seconds whoever asks.
type collector struct {
	node    *node
	dataDir string

	mu         sync.Mutex
	last       status
	at         time.Time
	subversion string
	lattice    bool // the node has given a lattice object before
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
		Tripwires: []tripwire{}, LatticeTripwires: []tripwire{}}
	var rpc *rpcClient
	s.Node, s.NodeError, rpc = c.node.snapshot()
	if rpc == nil || s.Node == "stopped" {
		if s.Node == "running" {
			s.Node = "noanswer" // the node has just exited; what follows is not known yet
		}
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
		switch {
		case errors.As(err, &answer) && answer.Code == rpcInWarmup:
			s.Warmup = answer.Message
		case s.Node == "running":
			s.Node = "noanswer"
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
		// Read on its own, so that a malformed lattice object leaves the rest of the answer as it is.
		Lattice json.RawMessage `json:"lattice"`
	}
	err := rpc.call("gettripwireinfo", nil, &tw)
	var answer *rpcError
	switch {
	case err == nil && tw.TipHash != "" && tw.TipHeight != nil && *tw.TipHeight >= 0 && tw.Height > 0 && tw.Tripwires != nil &&
		tw.ECDisabled != nil:
		s.TripwireInfo, s.H3 = "ok", tw.Height
		s.TripwireTip, s.TripwireTipHash = *tw.TipHeight, tw.TipHash
		s.Tripwires, s.ECDisabledNextBlock = tw.Tripwires, *tw.ECDisabled
	case errors.As(err, &answer) && answer.Code == rpcMethodNotFound:
		s.TripwireInfo = "absent"
	}
	c.readLattice(&s, tw.Lattice)
	tip := s.Blocks
	if s.TripwireInfo == "ok" && s.TripwireTip > tip {
		tip = s.TripwireTip
	}
	if s.ReachedH3 = tip >= s.H3; s.ReachedH3 {
		_ = rpc.call("getblockhash", []any{s.H3}, &s.BlockH3)
	}
	// M4-T follows the block its states are of, whatever getblockchaininfo said a moment before.
	if s.LatticeInfo == latticeOK {
		s.ReachedH4T = s.TripwireTip >= s.H4T
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

// readLattice reads the lattice object of gettripwireinfo, all or nothing, and only with the rest of the answer
// read: its states are of the same block. No object leaves M4-T out, unless the node gave one before; then, as a
// malformed one, it counts as no answer this time.
func (c *collector) readLattice(s *status, raw json.RawMessage) {
	var lattice struct {
		Height       int64      `json:"height"`
		Tripwires    []tripwire `json:"tripwires"`
		MLDSADisable *bool      `json:"mldsa_disabled_next_block"`
	}
	switch {
	case s.TripwireInfo == "ok" && len(raw) == 0:
		c.lattice = false
	case s.TripwireInfo == "ok" && json.Unmarshal(raw, &lattice) == nil && lattice.Height > 0 && lattice.Tripwires != nil &&
		latticeTripwiresValid(lattice.Tripwires) &&
		lattice.MLDSADisable != nil:
		c.lattice = true
		s.LatticeInfo, s.H4T = latticeOK, lattice.Height
		s.LatticeTripwires, s.MLDSADisabledNextBlock = lattice.Tripwires, *lattice.MLDSADisable
		return
	case s.TripwireInfo == "ok":
		c.lattice = true // a malformed object
	}
	if c.lattice {
		s.LatticeInfo = latticeUnknown
	}
}

// latticeTripwiresValid: every entry names an output and has one of the two states the node gives (a null entry
// decodes to an empty one).
func latticeTripwiresValid(wires []tripwire) bool {
	for _, w := range wires {
		if w.Outpoint == "" || (w.State != "unspent" && w.State != "missing") {
			return false
		}
	}
	return true
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
		node := s.Node
		if node == "noanswer" {
			node = "did not answer just now"
		}
		fmt.Fprintf(&b, "Node: %s\n", node)
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
	switch {
	case s.BlockH3 != "":
		fmt.Fprintf(&b, "Block %d (M3): %s\n", s.H3, s.BlockH3)
	case s.ReachedH3:
		fmt.Fprintf(&b, "Block %d (M3): reached, hash not read just now\n", s.H3)
	default:
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
	switch s.LatticeInfo {
	case latticeOK:
		if s.ReachedH4T {
			fmt.Fprintf(&b, "Block %d (M4-T): reached\n", s.H4T)
		} else {
			fmt.Fprintf(&b, "Block %d (M4-T): not reached yet\n", s.H4T)
		}
		for _, w := range s.LatticeTripwires {
			fmt.Fprintf(&b, "Lattice tripwire %s %s: %s\n", w.Label, w.Outpoint, w.State)
		}
		// The flag speaks of the next block. Before a next block under M4-T, leaves of version 0xe2 are not run at all.
		if s.TripwireTip+1 >= s.H4T {
			fmt.Fprintf(&b, "mldsa_disabled_next_block: %t\n", s.MLDSADisabledNextBlock)
		}
	case latticeUnknown:
		b.WriteString("Lattice tripwires (M4-T): unknown, the node did not answer just now\n")
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
