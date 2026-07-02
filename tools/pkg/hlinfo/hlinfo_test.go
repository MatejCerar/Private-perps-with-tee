package hlinfo

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func testClient(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestOpenPerpTrue(t *testing.T) {
	c := testClient(t, `{"assetPositions":[{"position":{"coin":"ETH","szi":"0.02"}}]}`)
	open, coin, szi, err := c.OpenPerp("0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if !open || coin != "ETH" || szi != 0.02 {
		t.Fatalf("got open=%v coin=%s szi=%v", open, coin, szi)
	}
}

func TestOpenPerpFalseWhenZero(t *testing.T) {
	c := testClient(t, `{"assetPositions":[{"position":{"coin":"ETH","szi":"0.0"}}]}`)
	open, _, _, err := c.OpenPerp("0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if open {
		t.Fatal("expected no open position when szi is 0")
	}
}

func TestOpenPerpFalseWhenEmpty(t *testing.T) {
	c := testClient(t, `{"assetPositions":[]}`)
	open, _, _, err := c.OpenPerp("0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if open {
		t.Fatal("expected no open position")
	}
}

func TestSpotUSDC(t *testing.T) {
	c := testClient(t, `{"balances":[{"coin":"USDC","total":"123.45"},{"coin":"ETH","total":"1.0"}]}`)
	bal, err := c.SpotUSDC("0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if bal != 123.45 {
		t.Fatalf("got %v", bal)
	}
}

func TestSpotUSDCZeroWhenAbsent(t *testing.T) {
	c := testClient(t, `{"balances":[{"coin":"ETH","total":"1.0"}]}`)
	bal, err := c.SpotUSDC("0xabc")
	if err != nil {
		t.Fatal(err)
	}
	if bal != 0 {
		t.Fatalf("got %v", bal)
	}
}
