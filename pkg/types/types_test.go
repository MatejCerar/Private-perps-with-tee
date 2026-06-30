package types

import (
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

func TestDecodeEnvelope(t *testing.T) {
	addrTy, _ := abi.NewType("address", "", nil)
	bytesTy, _ := abi.NewType("bytes", "", nil)
	args := abi.Arguments{{Type: addrTy}, {Type: bytesTy}}

	user := common.HexToAddress("0x1234567890123456789012345678901234567890")
	payload := []byte(`{"usdAmount":100}`)
	packed, err := args.Pack(user, payload)
	if err != nil {
		t.Fatal(err)
	}

	gotAddr, gotPayload, err := DecodeEnvelope(packed)
	if err != nil {
		t.Fatal(err)
	}
	if gotAddr != user {
		t.Fatalf("addr = %s", gotAddr.Hex())
	}
	if string(gotPayload) != string(payload) {
		t.Fatalf("payload = %s", gotPayload)
	}
}
