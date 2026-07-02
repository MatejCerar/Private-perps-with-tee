// Package hlinfo is a dependency-light client for Hyperliquid's public /info
// endpoint. The e2e test runner uses it to verify that on-chain instructions
// actually executed on Hyperliquid (positions, balances) for a given account,
// and to preflight the treasury.
package hlinfo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Client talks to a Hyperliquid info endpoint (e.g. https://api.hyperliquid-testnet.xyz).
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a Client for baseURL with a sane timeout.
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) post(body map[string]any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Post(c.BaseURL+"/info", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hyperliquid info %v: status %d", body["type"], resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type clearinghouseState struct {
	AssetPositions []struct {
		Position struct {
			Coin string `json:"coin"`
			Szi  string `json:"szi"`
		} `json:"position"`
	} `json:"assetPositions"`
}

// OpenPerp reports whether user has any non-zero perp position; when one exists
// it returns the first such coin and its signed size.
func (c *Client) OpenPerp(user string) (open bool, coin string, szi float64, err error) {
	var st clearinghouseState
	if err = c.post(map[string]any{"type": "clearinghouseState", "user": user}, &st); err != nil {
		return false, "", 0, err
	}
	for _, ap := range st.AssetPositions {
		v, perr := strconv.ParseFloat(ap.Position.Szi, 64)
		if perr != nil {
			continue
		}
		if v != 0 {
			return true, ap.Position.Coin, v, nil
		}
	}
	return false, "", 0, nil
}

type spotState struct {
	Balances []struct {
		Coin  string `json:"coin"`
		Total string `json:"total"`
	} `json:"balances"`
}

// SpotUSDC returns the user's total spot USDC balance (0 if none).
func (c *Client) SpotUSDC(user string) (float64, error) {
	var st spotState
	if err := c.post(map[string]any{"type": "spotClearinghouseState", "user": user}, &st); err != nil {
		return 0, err
	}
	for _, b := range st.Balances {
		if b.Coin == "USDC" {
			return strconv.ParseFloat(b.Total, 64)
		}
	}
	return 0, nil
}
