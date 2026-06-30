// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

// TODO: Replace local interfaces with imports from flare-smart-contracts-v2 once published as a package.
import { ITeeExtensionRegistry } from "./interfaces/ITeeExtensionRegistry.sol";
import { ITeeMachineRegistry } from "./interfaces/ITeeMachineRegistry.sol";

/// @title HyperliquidInstructionSender
/// @author Flare Foundation
/// @notice On-chain entry point for sending Hyperliquid instructions to the TEE.
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

    /// @notice Reference to the TEE extension registry contract.
    ITeeExtensionRegistry public immutable TEE_EXTENSION_REGISTRY;
    /// @notice Reference to the TEE machine registry contract.
    ITeeMachineRegistry public immutable TEE_MACHINE_REGISTRY;

    /// @notice First public extension ID. The registry reserves IDs below this
    /// for system/reserved extensions; public extensions are assigned from here up.
    uint256 private constant FIRST_PUBLIC_EXTENSION_ID = 0x10000; // 65536

    uint256 private _extensionId;

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

    /// @notice Credit the caller's TEE-managed account (mock deposit in v1).
    /// @param _payload JSON e.g. {"usdAmount":100}
    function deposit(bytes calldata _payload) external payable {
        _send(OP_COMMAND_DEPOSIT, _payload);
    }

    /// @notice Place a spot or perp order.
    /// @param _payload JSON e.g. {"market":"perp","coin":"BTC","isBuy":true,"orderType":"market","size":0.01,"leverage":5}
    function placeOrder(bytes calldata _payload) external payable {
        _send(OP_COMMAND_PLACE_ORDER, _payload);
    }

    /// @notice Cancel an open order.
    /// @param _payload JSON e.g. {"coin":"BTC","orderId":123}
    function cancel(bytes calldata _payload) external payable {
        _send(OP_COMMAND_CANCEL, _payload);
    }

    /// @notice Withdraw USD from the caller's TEE-managed account (internal stub in v1).
    /// @param _payload JSON e.g. {"usdAmount":50}
    function withdraw(bytes calldata _payload) external payable {
        _send(OP_COMMAND_WITHDRAW, _payload);
    }

    /// @notice Returns the cached extension ID, reverting if not yet set.
    /// @return The extension ID assigned to this contract.
    function _getExtensionId() internal view returns (uint256) {
        require(_extensionId != 0, "Extension ID is not set.");
        return _extensionId;
    }
}
