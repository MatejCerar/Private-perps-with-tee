// SPDX-License-Identifier: MIT
pragma solidity ^0.8.27;

/// @title TestUSDC — minimal mintable ERC20 standing in for USDC on Coston.
/// @notice Represents the user's Flare-side value in the testnet funding flow:
///         it is deposited into the Vault, and released back on withdrawal. The
///         Coston↔Arbitrum bridge is simulated, so this token is the on-chain
///         custody of record on the Flare side.
/// @dev 6 decimals to match real USDC. Anyone can mint (faucet). TESTNET ONLY.
contract TestUSDC {
    // forge-lint: disable-next-line(screaming-snake-case-const)
    string public constant name = "Test USD Coin";
    // forge-lint: disable-next-line(screaming-snake-case-const)
    string public constant symbol = "tUSDC";
    // ERC20 requires the getter to be named `decimals()`, so keep the lower-case name.
    // forge-lint: disable-next-line(screaming-snake-case-const)
    uint8 public constant decimals = 6;
    uint256 public totalSupply;

    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;

    event Transfer(address indexed from, address indexed to, uint256 value);
    event Approval(address indexed owner, address indexed spender, uint256 value);

    /// @notice Faucet: mint test tokens to any address. TESTNET ONLY.
    function mint(address to, uint256 amount) external {
        balanceOf[to] += amount;
        totalSupply += amount;
        emit Transfer(address(0), to, amount);
    }

    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        emit Approval(msg.sender, spender, amount);
        return true;
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        require(balanceOf[msg.sender] >= amount, "insufficient balance");
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
        emit Transfer(msg.sender, to, amount);
        return true;
    }

    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        require(allowance[from][msg.sender] >= amount, "insufficient allowance");
        require(balanceOf[from] >= amount, "insufficient balance");
        allowance[from][msg.sender] -= amount;
        balanceOf[from] -= amount;
        balanceOf[to] += amount;
        emit Transfer(from, to, amount);
        return true;
    }
}
