import { defineChain } from "viem";

export const coston = defineChain({
  id: 16,
  name: "Flare Coston",
  nativeCurrency: { name: "Coston Flare", symbol: "CFLR", decimals: 18 },
  rpcUrls: { default: { http: ["https://coston-api.flare.network/ext/C/rpc"] } },
  blockExplorers: {
    default: { name: "Coston Explorer", url: "https://coston-explorer.flare.network" },
  },
  testnet: true,
});

export const coston2 = defineChain({
  id: 114,
  name: "Flare Coston2",
  nativeCurrency: { name: "Coston2 Flare", symbol: "C2FLR", decimals: 18 },
  rpcUrls: { default: { http: ["https://coston2-api.flare.network/ext/C/rpc"] } },
  blockExplorers: {
    default: { name: "Coston2 Explorer", url: "https://coston2-explorer.flare.network" },
  },
  testnet: true,
});
