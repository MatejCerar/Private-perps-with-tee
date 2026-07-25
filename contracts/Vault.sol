// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

interface IERC20 {
    function transfer(address to, uint256 amount) external returns (bool);
}

/// @title Vault — on-chain custody of deposited test tokens on Coston.
/// @notice Tokens enter via HyperliquidInstructionSender.deposit() (transferFrom →
///         vault) and leave via executeWithdraw(), which anyone may call as long as
///         they hold a TEE-signed authorization for the (user, token, amount,
///         withdrawalId) tuple. The withdrawalId is single-use (replay protection),
///         and the signature must recover (EIP-191 personal_sign) to `teeAuthority`.
/// @dev Mirrors the shielded-transfer ShieldedVault. The Coston↔Arbitrum bridge is
///      simulated, so this vault holds the Flare-side value for the whole flow.
contract Vault {
    address public owner;
    address public teeAuthority;
    mapping(address => bool) public isAllowedToken;
    mapping(bytes32 => bool) public usedWithdrawals;

    event AllowedTokenUpdated(address indexed token, bool allowed);
    event TeeAuthoritySet(address indexed tee);
    event Released(address indexed user, address indexed token, uint256 amount, bytes32 indexed withdrawalId);
    event OwnerTransferred(address indexed previous, address indexed next);

    modifier onlyOwner() {
        _onlyOwner();
        _;
    }

    function _onlyOwner() internal view {
        require(msg.sender == owner, "not owner");
    }

    constructor(address _owner) {
        require(_owner != address(0), "owner zero");
        owner = _owner;
        emit OwnerTransferred(address(0), _owner);
    }

    function setAllowedToken(address token, bool allowed) external onlyOwner {
        require(token != address(0), "token zero");
        isAllowedToken[token] = allowed;
        emit AllowedTokenUpdated(token, allowed);
    }

    /// @notice Records the TEE's signing address. Settable once.
    function setTeeAuthority(address tee) external onlyOwner {
        require(teeAuthority == address(0), "tee authority already set");
        require(tee != address(0), "tee zero");
        teeAuthority = tee;
        emit TeeAuthoritySet(tee);
    }

    function transferOwner(address next) external onlyOwner {
        require(next != address(0), "next zero");
        emit OwnerTransferred(owner, next);
        owner = next;
    }

    /// @notice Releases tokens to `user` if the supplied signature was produced by the
    ///         TEE over the canonical authorization. Anyone may submit this.
    /// @param signature EIP-191 signature over
    ///        keccak256(abi.encodePacked(address(this), user, token, amount, withdrawalId))
    function executeWithdraw(
        address user,
        address token,
        uint256 amount,
        bytes32 withdrawalId,
        bytes calldata signature
    ) external {
        require(teeAuthority != address(0), "tee authority not set");
        require(amount > 0, "amount zero");
        require(isAllowedToken[token], "token not allowed");
        require(!usedWithdrawals[withdrawalId], "withdrawal already used");

        // forge-lint: disable-next-line(asm-keccak256)
        bytes32 hash = keccak256(abi.encodePacked(address(this), user, token, amount, withdrawalId));
        // forge-lint: disable-next-line(asm-keccak256)
        bytes32 ethSignedHash = keccak256(abi.encodePacked("\x19Ethereum Signed Message:\n32", hash));
        require(_recover(ethSignedHash, signature) == teeAuthority, "invalid signature");

        usedWithdrawals[withdrawalId] = true;

        require(IERC20(token).transfer(user, amount), "transfer failed");
        emit Released(user, token, amount, withdrawalId);
    }

    /// @dev Lifts a 65-byte (r||s||v) signature into ecrecover-friendly form.
    function _recover(bytes32 hash, bytes calldata sig) internal pure returns (address) {
        require(sig.length == 65, "invalid sig length");
        bytes32 r;
        bytes32 s;
        uint8 v;
        bytes memory sigMem = sig;
        assembly {
            r := mload(add(sigMem, 32))
            s := mload(add(sigMem, 64))
            v := byte(0, mload(add(sigMem, 96)))
        }
        if (v < 27) {
            v += 27;
        }
        require(v == 27 || v == 28, "invalid v");
        return ecrecover(hash, v, r, s);
    }
}
