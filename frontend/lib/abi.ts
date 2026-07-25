// Minimal ABI for HyperliquidInstructionSender (contracts/InstructionSender.sol).
// Every entry point takes a JSON payload encoded as bytes and is payable
// (the registry charges a per-instruction fee forwarded as msg.value).
export const instructionSenderAbi = [
  {
    type: "function",
    name: "deposit",
    stateMutability: "payable",
    inputs: [
      { name: "token", type: "address" },
      { name: "amount", type: "uint256" },
    ],
    outputs: [],
  },
  {
    type: "function",
    name: "requestWithdraw",
    stateMutability: "payable",
    inputs: [
      { name: "token", type: "address" },
      { name: "amount", type: "uint256" },
    ],
    outputs: [],
  },
  {
    type: "function",
    name: "placeOrder",
    stateMutability: "payable",
    inputs: [{ name: "_payload", type: "bytes" }],
    outputs: [],
  },
  {
    type: "function",
    name: "cancel",
    stateMutability: "payable",
    inputs: [{ name: "_payload", type: "bytes" }],
    outputs: [],
  },
  {
    type: "function",
    name: "confirmFunding",
    stateMutability: "payable",
    inputs: [
      { name: "token", type: "address" },
      { name: "amount", type: "uint256" },
    ],
    outputs: [],
  },
  {
    type: "function",
    name: "owner",
    stateMutability: "view",
    inputs: [],
    outputs: [{ name: "", type: "address" }],
  },
  {
    type: "function",
    name: "sponsoredLimit",
    stateMutability: "view",
    inputs: [{ name: "", type: "address" }],
    outputs: [{ name: "", type: "uint256" }],
  },
  {
    type: "function",
    name: "setSponsoredLimit",
    stateMutability: "nonpayable",
    inputs: [
      { name: "user", type: "address" },
      { name: "limit", type: "uint256" },
    ],
    outputs: [],
  },
  {
    type: "function",
    name: "isAdmin",
    stateMutability: "view",
    inputs: [{ name: "", type: "address" }],
    outputs: [{ name: "", type: "bool" }],
  },
  {
    type: "function",
    name: "setAdmin",
    stateMutability: "nonpayable",
    inputs: [
      { name: "admin", type: "address" },
      { name: "status", type: "bool" },
    ],
    outputs: [],
  },
] as const;

// TestUSDC (mintable ERC20, 6 decimals) on Coston.
export const testUsdcAbi = [
  {
    type: "function",
    name: "mint",
    stateMutability: "nonpayable",
    inputs: [
      { name: "to", type: "address" },
      { name: "amount", type: "uint256" },
    ],
    outputs: [],
  },
  {
    type: "function",
    name: "approve",
    stateMutability: "nonpayable",
    inputs: [
      { name: "spender", type: "address" },
      { name: "amount", type: "uint256" },
    ],
    outputs: [{ name: "", type: "bool" }],
  },
  {
    type: "function",
    name: "balanceOf",
    stateMutability: "view",
    inputs: [{ name: "", type: "address" }],
    outputs: [{ name: "", type: "uint256" }],
  },
  {
    type: "function",
    name: "allowance",
    stateMutability: "view",
    inputs: [
      { name: "", type: "address" },
      { name: "", type: "address" },
    ],
    outputs: [{ name: "", type: "uint256" }],
  },
] as const;

// Vault.executeWithdraw — releases escrowed TestUSDC against a TEE-signed slip.
export const vaultAbi = [
  {
    type: "function",
    name: "executeWithdraw",
    stateMutability: "nonpayable",
    inputs: [
      { name: "user", type: "address" },
      { name: "token", type: "address" },
      { name: "amount", type: "uint256" },
      { name: "withdrawalId", type: "bytes32" },
      { name: "signature", type: "bytes" },
    ],
    outputs: [],
  },
] as const;

// Emitted by the TeeExtensionRegistry when an instruction is submitted.
// `instructionId` (indexed, topic[2]) is the key the TEE proxy keys results on.
// This is the claimBackAddress-era variant, matching InstructionSender.sol's
// TeeInstructionParams. The indexed order is: extensionId, instructionId,
// rewardEpochId — so instructionId is always the 3rd topic regardless of the
// non-indexed tail.
export const teeInstructionsSentEvent = {
  type: "event",
  name: "TeeInstructionsSent",
  inputs: [
    { name: "extensionId", type: "uint256", indexed: true },
    { name: "instructionId", type: "bytes32", indexed: true },
    { name: "rewardEpochId", type: "uint32", indexed: true },
    {
      name: "teeMachines",
      type: "tuple[]",
      indexed: false,
      components: [
        { name: "signingAddress", type: "address" },
        { name: "proxyAddress", type: "address" },
        { name: "url", type: "string" },
      ],
    },
    { name: "opType", type: "bytes32", indexed: false },
    { name: "opCommand", type: "bytes32", indexed: false },
    { name: "message", type: "bytes", indexed: false },
    { name: "cosigners", type: "address[]", indexed: false },
    { name: "cosignersThreshold", type: "uint64", indexed: false },
    { name: "claimBackAddress", type: "address", indexed: false },
    { name: "fee", type: "uint256", indexed: false },
  ],
} as const;
