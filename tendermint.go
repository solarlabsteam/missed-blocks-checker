package main

import (
	"context"

	"github.com/rs/zerolog"
	tmrpc "github.com/tendermint/tendermint/rpc/client/http"
	ctypes "github.com/tendermint/tendermint/types"
)

type TendermintRPC struct {
	NodeConfig          NodeConfig
	BlocksDiffInThePast int64
	Logger              zerolog.Logger
}

func NewTendermintRPC(nodeConfig NodeConfig, logger *zerolog.Logger) *TendermintRPC {
	return &TendermintRPC{
		NodeConfig:          nodeConfig,
		BlocksDiffInThePast: 100,
		Logger:              logger.With().Str("component", "rpc").Logger(),
	}
}

func (rpc *TendermintRPC) GetAvgBlockTime() float64 {
	latestBlock := rpc.GetBlock(nil)
	latestHeight := latestBlock.Height
	beforeLatestBlockHeight := latestBlock.Height - rpc.BlocksDiffInThePast
	beforeLatestBlock := rpc.GetBlock(&beforeLatestBlockHeight)

	heightDiff := float64(latestHeight - beforeLatestBlockHeight)
	timeDiff := latestBlock.Time.Sub(beforeLatestBlock.Time).Seconds()

	return timeDiff / heightDiff
}

func (rpc *TendermintRPC) GetBlock(height *int64) *ctypes.Block {
	client, err := tmrpc.New(rpc.NodeConfig.TendermintRPC, "/websocket")
	if err != nil {
		rpc.Logger.Fatal().Err(err).Msg("Could not create Tendermint client")
	}

	block, err := client.Block(context.Background(), height)
	if err != nil {
		rpc.Logger.Fatal().Err(err).Msg("Could not query Tendermint status")
	}

	return block.Block
}
