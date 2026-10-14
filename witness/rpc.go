package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// RPC error codes of Bitcoin Core that the program tells apart.
const (
	rpcInWarmup       = -28    // the node is still loading
	rpcMethodNotFound = -32601 // a node without that call, such as one built without the M3 patches
)

// errNotReady means the node does not answer: not started yet, still writing its cookie, or gone.
var errNotReady = errors.New("the node is not ready")

// rpcError is an error the node answered with.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

// rpcClient calls the node's JSON-RPC on 127.0.0.1 with the cookie the node writes into its data folder.
type rpcClient struct {
	url        string
	cookiePath string
	client     *http.Client
}

func newRPCClient(port int, cookiePath string) *rpcClient {
	return &rpcClient{
		url:        "http://127.0.0.1:" + strconv.Itoa(port) + "/",
		cookiePath: cookiePath,
		// No proxy: the node is on this computer, whatever the environment says.
		client: &http.Client{Transport: &http.Transport{Proxy: nil}},
	}
}

func (c *rpcClient) call(method string, params []any, out any) error {
	return c.callWithin(60*time.Second, method, params, out)
}

// callWithin runs one call and decodes its result into out, which may be nil.
func (c *rpcClient) callWithin(timeout time.Duration, method string, params []any, out any) error {
	cookie, err := os.ReadFile(c.cookiePath)
	if err != nil {
		return errNotReady
	}
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "1.0", "id": "witness", "method": method, "params": params})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(bytes.TrimSpace(cookie)))
	resp, err := c.client.Do(req)
	if err != nil {
		return errNotReady
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return errNotReady
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	// A refused cookie comes back as 401 with an empty body: that, or anything but JSON-RPC, is no answer.
	if err := json.Unmarshal(data, &reply); err != nil {
		return errNotReady
	}
	if reply.Error != nil {
		return reply.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(reply.Result, out)
}

// alive reports whether the node of this data folder answers on this port, even if only to say it is loading.
func (c *rpcClient) alive() bool {
	err := c.callWithin(3*time.Second, "getblockcount", nil, nil)
	var answer *rpcError
	return err == nil || errors.As(err, &answer)
}
