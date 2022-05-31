package main

import (
	"fmt"
	"time"

	"github.com/cosmos/cosmos-sdk/simapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"
)

type Params struct {
	AvgBlockTime       float64
	SignedBlocksWindow int64
	MissedBlocksToJail int64
}

func Execute(configPath string) {
	appConfig, err := LoadConfig(configPath)
	if err != nil {
		GetDefaultLogger().Fatal().Err(err).Msg("Could not load config")
	}

	appConfig.Validate()        // will exit if not valid
	appConfig.SetBechPrefixes() // will exit if not valid
	SetSdkConfigPrefixes(appConfig)

	log := GetLogger(appConfig.LogConfig)

	log.Info().
		Str("config", fmt.Sprintf("%+v", appConfig)).
		Msg("Started with following parameters")

	if len(appConfig.IncludeValidators) == 0 && len(appConfig.ExcludeValidators) == 0 {
		log.Info().Msg("Monitoring all validators")
	} else if len(appConfig.IncludeValidators) != 0 {
		log.Info().
			Strs("validators", appConfig.IncludeValidators).
			Msg("Monitoring specific validators")
	} else {
		log.Info().
			Strs("validators", appConfig.ExcludeValidators).
			Msg("Monitoring all validators except specific")
	}

	encCfg := simapp.MakeTestEncodingConfig()
	interfaceRegistry := encCfg.InterfaceRegistry

	rpc := NewTendermintRPC(appConfig.TendermintRPC, log)
	grpc := NewTendermintGRPC(appConfig.NodeAddress, appConfig.Limit, interfaceRegistry, log)
	slashingParams := grpc.GetSlashingParams()

	params := Params{
		AvgBlockTime:       rpc.GetAvgBlockTime(),
		SignedBlocksWindow: slashingParams.SignedBlocksWindow,
		MissedBlocksToJail: slashingParams.MissedBlocksToJail,
	}

	log.Info().
		Int64("missedBlocksToJail", params.MissedBlocksToJail).
		Float64("avgBlockTime", params.AvgBlockTime).
		Msg("Chain params calculated")

	appConfig.SetDefaultMissedBlocksGroups(params)

	log.Info().
		Str("groups", fmt.Sprintf("%+v", appConfig.MissedBlocksGroups)).
		Msg("Using the following MissedBlocksGroups")

	if err := appConfig.MissedBlocksGroups.Validate(params.SignedBlocksWindow); err != nil {
		log.Fatal().Err(err).Msg("MissedBlockGroups config is invalid")
	}

	reporters := []Reporter{
		NewTelegramReporter(appConfig.ChainInfoConfig, appConfig.TelegramConfig, appConfig, &params, grpc, log),
		NewSlackReporter(appConfig.ChainInfoConfig, appConfig.SlackConfig, &params, log),
	}

	for _, reporter := range reporters {
		reporter.Init()

		if reporter.Enabled() {
			log.Info().Str("name", reporter.Name()).Msg("Init reporter")
		}
	}

	reportGenerator := NewReportGenerator(params, grpc, appConfig, log, interfaceRegistry)

	for {
		report := reportGenerator.GenerateReport()
		if report == nil || len(report.Entries) == 0 {
			log.Info().Msg("Report is empty, not sending.")
			time.Sleep(time.Duration(appConfig.Interval) * time.Second)
			continue
		}

		for _, reporter := range reporters {
			if !reporter.Enabled() {
				log.Debug().Str("name", reporter.Name()).Msg("Reporter is disabled.")
				continue
			}

			log.Info().Str("name", reporter.Name()).Msg("Sending a report to reporter...")
			if err := reporter.SendReport(*report); err != nil {
				log.Error().Err(err).Str("name", reporter.Name()).Msg("Could not send message")
			}
		}

		time.Sleep(time.Duration(appConfig.Interval) * time.Second)
	}
}

func SetSdkConfigPrefixes(appConfig *AppConfig) {
	config := sdk.GetConfig()
	config.SetBech32PrefixForValidator(appConfig.ValidatorPrefix, appConfig.ValidatorPubkeyPrefix)
	config.SetBech32PrefixForConsensusNode(appConfig.ConsensusNodePrefix, appConfig.ConsensusNodePubkeyPrefix)
	config.Seal()
}

func main() {
	var ConfigPath string

	rootCmd := &cobra.Command{
		Use:  "tendermint-exporter",
		Long: "Scrape the data on Tendermint node.",
		Run: func(cmd *cobra.Command, args []string) {
			Execute(ConfigPath)
		},
	}

	rootCmd.PersistentFlags().StringVar(&ConfigPath, "config", "", "Config file path")
	if err := rootCmd.MarkPersistentFlagRequired("config"); err != nil {
		GetDefaultLogger().Fatal().Err(err).Msg("Could not set flags")
	}

	if err := rootCmd.Execute(); err != nil {
		GetDefaultLogger().Fatal().Err(err).Msg("Could not start application")
	}
}
