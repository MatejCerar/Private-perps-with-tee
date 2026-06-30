package types

import "extension-scaffold/pkg/decoder"

// RegisterDecoders registers JSON decoders for HL command messages and results.
func RegisterDecoders(r *decoder.Registry) {
	// messages
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "DEPOSIT", Kind: decoder.KindMessage}, decoder.NewJSONDecoder[DepositRequest]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "PLACE_ORDER", Kind: decoder.KindMessage}, decoder.NewJSONDecoder[PlaceOrderRequest]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "CANCEL", Kind: decoder.KindMessage}, decoder.NewJSONDecoder[CancelRequest]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "WITHDRAW", Kind: decoder.KindMessage}, decoder.NewJSONDecoder[WithdrawRequest]())
	// results
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "DEPOSIT", Kind: decoder.KindResult}, decoder.NewJSONDecoder[DepositResponse]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "PLACE_ORDER", Kind: decoder.KindResult}, decoder.NewJSONDecoder[OrderResponse]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "CANCEL", Kind: decoder.KindResult}, decoder.NewJSONDecoder[CancelResponse]())
	r.Register(decoder.RegistryKey{OPType: "HL", OPCommand: "WITHDRAW", Kind: decoder.KindResult}, decoder.NewJSONDecoder[WithdrawResponse]())
}
