package main

import (
	"context"
	"strconv"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
)

type TendermintGRPC struct {
	NodeAddress string
	Limit       uint64
	Client      *grpc.ClientConn
	Logger      zerolog.Logger
	Registry    codectypes.InterfaceRegistry
}

func NewTendermintGRPC(node string, limit uint64, registry codectypes.InterfaceRegistry, logger *zerolog.Logger) *TendermintGRPC {
	grpcConn, err := grpc.Dial(
		node,
		grpc.WithInsecure(),
	)
	if err != nil {
		GetDefaultLogger().Fatal().Err(err).Msg("Could not establish gRPC connection")
	}

	return &TendermintGRPC{
		NodeAddress: node,
		Limit:       limit,
		Logger:      logger.With().Str("component", "grpc").Logger(),
		Client:      grpcConn,
		Registry:    registry,
	}
}

type SlashingParams struct {
	SignedBlocksWindow int64
	MinSignedPerWindow float64
	MissedBlocksToJail int64
}

func (grpc *TendermintGRPC) GetSlashingParams() SlashingParams {
	slashingClient := slashingtypes.NewQueryClient(grpc.Client)
	params, err := slashingClient.Params(
		context.Background(),
		&slashingtypes.QueryParamsRequest{},
	)
	if err != nil {
		grpc.Logger.Fatal().Err(err).Msg("Could not query slashing params")
	}

	minSignedPerWindow, err := strconv.ParseFloat(params.Params.MinSignedPerWindow.String(), 64)
	if err != nil {
		grpc.Logger.Fatal().
			Err(err).
			Msg("Could not parse min signed per window")
	}

	return SlashingParams{
		SignedBlocksWindow: params.Params.SignedBlocksWindow,
		MinSignedPerWindow: minSignedPerWindow,
		MissedBlocksToJail: int64(float64(params.Params.SignedBlocksWindow) * (1 - minSignedPerWindow)),
	}
}

func (grpc *TendermintGRPC) GetSigningInfos() ([]slashingtypes.ValidatorSigningInfo, error) {
	slashingClient := slashingtypes.NewQueryClient(grpc.Client)
	signingInfos, err := slashingClient.SigningInfos(
		context.Background(),
		&slashingtypes.QuerySigningInfosRequest{
			Pagination: &querytypes.PageRequest{
				Limit: grpc.Limit,
			},
		},
	)
	if err != nil {
		grpc.Logger.Error().Err(err).Msg("Could not query for signing info")
		return nil, err
	}

	return signingInfos.Info, nil
}

func (grpc *TendermintGRPC) GetValidators() ([]stakingtypes.Validator, error) {
	stakingClient := stakingtypes.NewQueryClient(grpc.Client)
	validatorsResult, err := stakingClient.Validators(
		context.Background(),
		&stakingtypes.QueryValidatorsRequest{
			Pagination: &querytypes.PageRequest{
				Limit: grpc.Limit,
			},
		},
	)
	if err != nil {
		grpc.Logger.Error().Err(err).Msg("Could not query for validators")
		return nil, err
	}

	return validatorsResult.Validators, nil
}

func (grpc *TendermintGRPC) GetValidator(address string) (stakingtypes.Validator, error) {
	stakingClient := stakingtypes.NewQueryClient(grpc.Client)

	validatorResponse, err := stakingClient.Validator(
		context.Background(),
		&stakingtypes.QueryValidatorRequest{ValidatorAddr: address},
	)
	if err != nil {
		return stakingtypes.Validator{}, err
	}

	return validatorResponse.Validator, nil
}

func (grpc *TendermintGRPC) GetSigningInfo(validator stakingtypes.Validator) (slashingtypes.ValidatorSigningInfo, error) {
	slashingClient := slashingtypes.NewQueryClient(grpc.Client)

	err := validator.UnpackInterfaces(grpc.Registry) // Unpack interfaces, to populate the Anys' cached values
	if err != nil {
		grpc.Logger.Error().
			Str("address", validator.OperatorAddress).
			Err(err).
			Msg("Could not get unpack validator inferfaces")
		return slashingtypes.ValidatorSigningInfo{}, err
	}

	pubKey, err := validator.GetConsAddr()
	if err != nil {
		grpc.Logger.Error().
			Str("address", validator.OperatorAddress).
			Err(err).
			Msg("Could not get validator pubkey")
		return slashingtypes.ValidatorSigningInfo{}, err
	}

	signingInfosResponse, err := slashingClient.SigningInfo(
		context.Background(),
		&slashingtypes.QuerySigningInfoRequest{ConsAddress: pubKey.String()},
	)
	if err != nil {
		grpc.Logger.Error().
			Str("address", validator.OperatorAddress).
			Err(err).
			Msg("Could not get signing info")
		return slashingtypes.ValidatorSigningInfo{}, err
	}

	return signingInfosResponse.ValSigningInfo, nil
}
