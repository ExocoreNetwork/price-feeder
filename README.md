# Price Feeder

# Overview

The price feeder is a tool that validators use to submit oracle prices to Exocore nodes.

This tool fetches token prices from off-chain sources such as `CEX` and `DEX`, then submits this price information to the Exocore blockchain network as a standard Cosmos SDK transaction. Once the submitted prices from validators reach the voting power threshold, they achieve consensus on-chain and become the final price.

## Workflow

1. Subscribe to a new block.
2. Retrieve prices from off-chain sources.
3. Submit a transaction with the price data to update the oracle price on-chain.

# Installation

`make install` 

Or check out the latest release.

# Quick Start

`pricefeeder --config path/to/config --sources path/to/sources/config start -m [mnemonic of validator's consensus key]`
