// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

// TODO: Replace local interfaces with imports from flare-smart-contracts-v2 once published as a package.
import { ITeeExtensionRegistry } from "./interfaces/ITeeExtensionRegistry.sol";
import { ITeeMachineRegistry } from "./interfaces/ITeeMachineRegistry.sol";

interface IERC20 {
    function transferFrom(address from, address to, uint256 amount) external returns (bool);
}

interface IVault {
    function isAllowedToken(address token) external view returns (bool);
}

/// @title HyperliquidInstructionSender
/// @author Flare Foundation
/// @notice On-chain entry point for sending Hyperliquid instructions to the TEE.
///
/// DEPOSIT/WITHDRAW move real test tokens through a Coston Vault (like the
/// shielded-transfer example): a deposit escrows tokens into the vault and emits
/// a DEPOSIT instruction carrying (token, amount); a withdrawal emits a WITHDRAW
/// instruction and the TEE later signs a slip the vault verifies to release funds.
/// PLACE_ORDER/CANCEL stay as raw JSON payloads built off-chain.
///
/// DO NOT MODIFY: constructor, setExtensionId(), _getExtensionId()
contract HyperliquidInstructionSender {
    /// @notice Operation type for Hyperliquid actions.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_TYPE_HL = bytes32("HL");

    /// @notice Command for the DEPOSIT action.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_DEPOSIT = bytes32("DEPOSIT");

    /// @notice Command for the PLACE_ORDER action.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_PLACE_ORDER = bytes32("PLACE_ORDER");

    /// @notice Command for the CANCEL action.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_CANCEL = bytes32("CANCEL");

    /// @notice Command for the WITHDRAW action.
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_WITHDRAW = bytes32("WITHDRAW");

    /// @notice Command for the CONFIRM_FUNDING action (self-fund users confirm
    /// they funded their HL address; the TEE verifies the balance and credits).
    // forge-lint: disable-next-line(unsafe-typecast)
    bytes32 public constant OP_COMMAND_CONFIRM_FUNDING = bytes32("CONFIRM_FUNDING");

    /// @notice Reference to the TEE extension registry contract.
    ITeeExtensionRegistry public immutable TEE_EXTENSION_REGISTRY;
    /// @notice Reference to the TEE machine registry contract.
    ITeeMachineRegistry public immutable TEE_MACHINE_REGISTRY;

    /// @notice First public extension ID. The registry reserves IDs below this
    /// for system/reserved extensions; public extensions are assigned from here up.
    uint256 private constant FIRST_PUBLIC_EXTENSION_ID = 0x10000; // 65536

    uint256 private _extensionId;

    /// @notice Coston Vault that escrows deposited tokens. Set once post-deploy.
    address public vault;

    /// @notice Contract owner (deployer). Manages admins and the sponsored allowlist.
    address public owner;

    /// @notice Admins can call setSponsoredLimit. The owner is always an admin.
    mapping(address => bool) public isAdmin;

    /// @notice Per-user sponsored funding limit in token units (6 decimals for
    /// TestUSDC). Zero means not sponsored — the user must self-fund. Non-zero
    /// means the treasury will fund deposits up to this cumulative limit.
    mapping(address => uint256) public sponsoredLimit;

    event VaultSet(address indexed vault);
    event SponsoredLimitSet(address indexed user, uint256 limit);
    event AdminSet(address indexed admin, bool status);

    modifier onlyOwner() {
        require(msg.sender == owner, "not owner");
        _;
    }

    modifier onlyAdmin() {
        require(msg.sender == owner || isAdmin[msg.sender], "not admin");
        _;
    }

    /// @notice Initializes the contract with registry addresses.
    /// @param _teeExtensionRegistry Address of the TEE extension registry.
    /// @param _teeMachineRegistry Address of the TEE machine registry.
    constructor(
        ITeeExtensionRegistry _teeExtensionRegistry,
        ITeeMachineRegistry _teeMachineRegistry
    ) {
        require(address(_teeExtensionRegistry) != address(0), "TeeExtensionRegistry cannot be zero address");
        require(address(_teeMachineRegistry) != address(0), "TeeMachineRegistry cannot be zero address");
        require(address(_teeExtensionRegistry).code.length > 0, "TeeExtensionRegistry has no code");
        require(address(_teeMachineRegistry).code.length > 0, "TeeMachineRegistry has no code");
        TEE_EXTENSION_REGISTRY = _teeExtensionRegistry;
        TEE_MACHINE_REGISTRY = _teeMachineRegistry;
        owner = msg.sender;
    }

    /// @notice Finds and sets this contract's extension id. Can only be set once.
    /// DO NOT MODIFY this function.
    function setExtensionId() external {
        require(_extensionId == 0, "Extension ID already set.");

        uint256 c = TEE_EXTENSION_REGISTRY.nextPublicExtensionId();
        for (uint256 i = FIRST_PUBLIC_EXTENSION_ID; i < c; ++i) {
            if (TEE_EXTENSION_REGISTRY.getTeeExtensionInstructionsSender(i) == address(this)) {
                _extensionId = i;
                return;
            }
        }
        revert("Extension ID not found.");
    }

    /// @notice Wires the Vault. Settable once (post-deploy, alongside setExtensionId).
    function setVault(address _vault) external {
        require(vault == address(0), "vault already set");
        require(_vault != address(0), "vault zero");
        require(_vault.code.length > 0, "vault has no code");
        vault = _vault;
        emit VaultSet(_vault);
    }

    /// @notice Set a user's sponsored funding limit (in token units, 6 decimals).
    /// Zero revokes sponsorship; non-zero lets the treasury fund deposits up to
    /// this cumulative limit. Example: 5000e6 = $5,000.
    function setSponsoredLimit(address user, uint256 limit) external onlyAdmin {
        sponsoredLimit[user] = limit;
        emit SponsoredLimitSet(user, limit);
    }

    /// @notice Grant or revoke admin status. Admins can call setSponsoredLimit.
    /// The owner is always an admin and cannot be removed.
    function setAdmin(address admin, bool status) external onlyOwner {
        isAdmin[admin] = status;
        emit AdminSet(admin, status);
    }

    /// @notice Emits an HL instruction, embedding msg.sender so the TEE can
    /// derive the caller's dedicated Hyperliquid account.
    function _send(bytes32 opCommand, bytes memory _payload) internal {
        address[] memory teeIds = TEE_MACHINE_REGISTRY.getRandomTeeIds(_getExtensionId(), 1);
        address[] memory cosigners = new address[](0);

        bytes memory message = abi.encode(msg.sender, _payload);

        ITeeExtensionRegistry.TeeInstructionParams memory params = ITeeExtensionRegistry.TeeInstructionParams({
            opType: OP_TYPE_HL,
            opCommand: opCommand,
            message: message,
            cosigners: cosigners,
            cosignersThreshold: 0,
            claimBackAddress: msg.sender
        });

        TEE_EXTENSION_REGISTRY.sendInstructions{value: msg.value}(teeIds, params);
    }

    /// @notice Deposit test tokens: escrow into the Vault and relay a DEPOSIT
    /// instruction carrying (token, amount, sponsoredLimit). Caller must approve
    /// this contract first. If sponsoredLimit > 0 and the user's cumulative
    /// deposits are within the limit, the treasury funds them; otherwise they
    /// receive their HL address to self-fund.
    /// @param token The ERC20 to escrow (must be on the Vault's allow-list).
    /// @param amount Token amount in the token's own units.
    function deposit(address token, uint256 amount) external payable {
        require(vault != address(0), "vault not set");
        require(IVault(vault).isAllowedToken(token), "token not allowed");
        require(amount > 0, "amount zero");

        require(IERC20(token).transferFrom(msg.sender, vault, amount), "transferFrom failed");

        _send(OP_COMMAND_DEPOSIT, abi.encode(token, amount, sponsoredLimit[msg.sender]));
    }

    /// @notice Confirm self-funding: the caller funded their HL address (derived
    /// by the TEE from their Flare address) via the HL testnet faucet or bridge.
    /// The TEE verifies the HL balance and credits the ledger.
    /// @param token The token originally escrowed in the deposit.
    /// @param amount The amount the user claims to have funded (token units).
    function confirmFunding(address token, uint256 amount) external payable {
        require(amount > 0, "amount zero");
        _send(OP_COMMAND_CONFIRM_FUNDING, abi.encode(token, amount));
    }

    /// @notice Request a withdrawal of (token, amount). The TEE debits the ledger,
    /// unwinds on HL, and returns a signed slip the Vault verifies in executeWithdraw.
    function requestWithdraw(address token, uint256 amount) external payable {
        require(amount > 0, "amount zero");
        _send(OP_COMMAND_WITHDRAW, abi.encode(token, amount));
    }

    /// @notice Place a spot or perp order. Raw JSON payload, built off-chain.
    /// @param _payload JSON e.g. {"market":"perp","coin":"BTC","isBuy":true,"orderType":"market","size":0.01,"leverage":5}
    function placeOrder(bytes calldata _payload) external payable {
        _send(OP_COMMAND_PLACE_ORDER, _payload);
    }

    /// @notice Cancel an open order.
    /// @param _payload JSON e.g. {"coin":"BTC","orderId":123}
    function cancel(bytes calldata _payload) external payable {
        _send(OP_COMMAND_CANCEL, _payload);
    }

    /// @notice Returns the cached extension ID, reverting if not yet set.
    /// @return The extension ID assigned to this contract.
    function _getExtensionId() internal view returns (uint256) {
        require(_extensionId != 0, "Extension ID is not set.");
        return _extensionId;
    }
}
