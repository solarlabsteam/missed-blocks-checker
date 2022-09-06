package main

import (
	"context"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type TendermintGRPC struct {
	NodeConfig           NodeConfig
	Limit                uint64
	Client               *grpc.ClientConn
	Logger               zerolog.Logger
	Registry             codectypes.InterfaceRegistry
	QueryEachSigningInfo bool
}

func NewTendermintGRPC(
	nodeConfig NodeConfig,
	registry codectypes.InterfaceRegistry,
	queryEachSigningInfo bool,
	logger *zerolog.Logger,
) *TendermintGRPC {
	grpcConn, err := grpc.Dial(
		nodeConfig.GrpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		GetDefaultLogger().Fatal().Err(err).Msg("Could not establish gRPC connection")
	}

	return &TendermintGRPC{
		NodeConfig:           nodeConfig,
		Limit:                1000,
		Logger:               logger.With().Str("component", "grpc").Logger(),
		Client:               grpcConn,
		Registry:             registry,
		QueryEachSigningInfo: queryEachSigningInfo,
	}
}

type SlashingParams struct {
	SignedBlocksWindow      int64
	MinSignedPerWindow      float64
	MissedBlocksToJail      int64
	DowntimeJailDuration    time.Duration
	SlashFractionDoubleSign float64
	SlashFractionDowntime   float64
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

	minSignedPerWindow := params.Params.MinSignedPerWindow.MustFloat64()

	return SlashingParams{
		SignedBlocksWindow:      params.Params.SignedBlocksWindow,
		MinSignedPerWindow:      minSignedPerWindow,
		MissedBlocksToJail:      int64(float64(params.Params.SignedBlocksWindow) * (1 - minSignedPerWindow)),
		DowntimeJailDuration:    params.Params.DowntimeJailDuration,
		SlashFractionDoubleSign: params.Params.SlashFractionDoubleSign.MustFloat64(),
		SlashFractionDowntime:   params.Params.SlashFractionDowntime.MustFloat64(),
	}
}

func (grpc *TendermintGRPC) GetValidatorsState() (ValidatorsState, error) {
	if grpc.QueryEachSigningInfo {
		return grpc.GetValidatorsStateWithEachSigningInfo()
	}

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

	validatorsMap := make(map[string]stakingtypes.Validator, len(validatorsResult.Validators))
	for _, validator := range validatorsResult.Validators {
		err := validator.UnpackInterfaces(grpc.Registry)
		if err != nil {
			grpc.Logger.Error().Err(err).Msg("Could not unpack interface")
			return nil, err
		}

		pubKey, err := validator.GetConsAddr()
		if err != nil {
			grpc.Logger.Error().Err(err).Msg("Could not get cons addr")
			return nil, err
		}

		validatorsMap[pubKey.String()] = validator
	}

	newState := make(ValidatorsState, len(signingInfos.Info))

	for _, info := range signingInfos.Info {
		validator, ok := validatorsMap[info.Address]
		if !ok {
			grpc.Logger.Warn().Str("address", info.Address).Msg("Could not find validator by pubkey")
			continue
		}

		newState[info.Address] = NewValidatorState(validator, info)
	}

	return newState, nil
}

func (grpc *TendermintGRPC) GetValidatorsStateWithEachSigningInfo() (ValidatorsState, error) {
	slashingClient := slashingtypes.NewQueryClient(grpc.Client)
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

	newState := make(ValidatorsState, len(validatorsResult.Validators))
	for _, validator := range validatorsResult.Validators {
		err := validator.UnpackInterfaces(grpc.Registry)
		if err != nil {
			grpc.Logger.Error().Err(err).Msg("Could not unpack interface")
			return nil, err
		}

		pubKey, err := validator.GetConsAddr()
		if err != nil {
			grpc.Logger.Error().Err(err).Msg("Could not get cons addr")
			return nil, err
		}

		info, err := slashingClient.SigningInfo(
			context.Background(),
			&slashingtypes.QuerySigningInfoRequest{
				ConsAddress: pubKey.String(),
			},
		)

		if err != nil {
			grpc.Logger.Error().Err(err).Msg("Could not query for signing info")
			continue
		}

		newState[pubKey.String()] = NewValidatorState(validator, info.ValSigningInfo)
	}

	return newState, nil
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

func (grpc *TendermintGRPC) GetValidatorState(address string) (ValidatorState, error) {
	stakingClient := stakingtypes.NewQueryClient(grpc.Client)

	validatorResponse, err := stakingClient.Validator(
		context.Background(),
		&stakingtypes.QueryValidatorRequest{ValidatorAddr: address},
	)
	if err != nil {
		return ValidatorState{}, err
	}

	validator := validatorResponse.Validator
	slashingClient := slashingtypes.NewQueryClient(grpc.Client)

	err = validator.UnpackInterfaces(grpc.Registry) // Unpack interfaces, to populate the Anys' cached values
	if err != nil {
		grpc.Logger.Error().
			Str("address", validator.OperatorAddress).
			Err(err).
			Msg("Could not get unpack validator inferfaces")
		return ValidatorState{}, err
	}

	pubKey, err := validator.GetConsAddr()
	if err != nil {
		grpc.Logger.Error().
			Str("address", validator.OperatorAddress).
			Err(err).
			Msg("Could not get validator pubkey")
		return ValidatorState{}, err
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
		return ValidatorState{}, err
	}

	return NewValidatorState(validator, signingInfosResponse.ValSigningInfo), nil
}
