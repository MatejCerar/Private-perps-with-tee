"use client";

import { WagmiProvider, createConfig, http } from "wagmi";
import { injected } from "wagmi/connectors";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { coston, coston2 } from "@/lib/chains";

// Register both testnets so the config is well-typed regardless of which one
// NEXT_PUBLIC_CHAIN selects; the UI targets `activeChain` and guards mismatches.
const wagmiConfig = createConfig({
  chains: [coston, coston2],
  connectors: [injected()],
  transports: {
    [coston.id]: http(),
    [coston2.id]: http(),
  },
  ssr: true,
});

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(() => new QueryClient());
  return (
    <WagmiProvider config={wagmiConfig}>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </WagmiProvider>
  );
}
