package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"google.golang.org/grpc"

	"github.com/cosmos/cosmos-sdk/simapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	tmrpc "github.com/tendermint/tendermint/rpc/client/http"
	ctypes "github.com/tendermint/tendermint/types"

	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/rs/zerolog"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	Config AppConfig

	BlocksDiffInThePast int64 = 100
	AvgBlockTime        float64
	SignedBlocksWindow  int64
	MissedBlocksToJail  int64

	grpcConn *grpc.ClientConn

	State ValidatorsState = make(map[string]ValidatorState)
)

var reporters []Reporter

var log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout}).With().Timestamp().Logger()

var (
	encCfg            = simapp.MakeTestEncodingConfig()
	interfaceRegistry = encCfg.InterfaceRegistry
)

var rootCmd = &cobra.Command{
	Use:  "missed-blocks-checker",
	Long: "Tool to monitor missed blocks for Cosmos-chain validators",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if Config.ConfigPath == "" {
			SetBechPrefixes(cmd)
			return nil
		}

		viper.SetConfigFile(Config.ConfigPath)
		if err := viper.ReadInConfig(); err != nil {
			log.Info().Err(err).Msg("Error reading config file")
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				return err
			}
		}

		// Credits to https://carolynvanslyck.com/blog/2020/08/sting-of-the-viper/
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if !f.Changed && viper.IsSet(f.Name) {
				val := viper.Get(f.Name)
				if err := cmd.Flags().Set(f.Name, fmt.Sprintf("%v", val)); err != nil {
					log.Fatal().Err(err).Msg("Could not set flag")
				}
			}
		})

		SetBechPrefixes(cmd)

		return nil
	},
	Run: Execute,
}

func Execute(cmd *cobra.Command, args []string) {
	logLevel, err := zerolog.ParseLevel(Config.LogLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not parse log level")
	}

	if Config.JsonOutput {
		log = zerolog.New(os.Stdout).With().Timestamp().Logger()
	}

	zerolog.SetGlobalLevel(logLevel)

	config := sdk.GetConfig()
	config.SetBech32PrefixForValidator(Config.ValidatorPrefix, Config.ValidatorPubkeyPrefix)
	config.SetBech32PrefixForConsensusNode(Config.ConsensusNodePrefix, Config.ConsensusNodePubkeyPrefix)
	config.Seal()

	log.Info().
		Str("config", fmt.Sprintf("%+v", Config)).
		Msg("Started with following parameters")

	if len(Config.IncludeValidators) != 0 && len(Config.ExcludeValidators) != 0 {
		log.Fatal().Msg("Cannot use --include and --exclude at the same time!")
	}

	if len(Config.IncludeValidators) == 0 && len(Config.ExcludeValidators) == 0 {
		log.Info().Msg("Monitoring all validators")
	} else if len(Config.IncludeValidators) != 0 {
		log.Info().
			Strs("validators", Config.IncludeValidators).
			Msg("Monitoring specific validators")
	} else {
		log.Info().
			Strs("validators", Config.ExcludeValidators).
			Msg("Monitoring all validators except specific")
	}

	grpcConn, err = grpc.Dial(
		Config.NodeAddress,
		grpc.WithInsecure(),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not establish gRPC connection")
	}

	defer grpcConn.Close()

	SetAvgBlockTime()
	SetMissedBlocksToJail()
	SetDefaultMissedBlocksGroups()

	log.Info().
		Str("groups", fmt.Sprintf("%+v", Config.MissedBlocksGroups)).
		Msg("Using the following MissedBlocksGroups")

	if err := Config.MissedBlocksGroups.Validate(MissedBlocksToJail); err != nil {
		log.Fatal().Err(err).Msg("MissedBlockGroups config is invalid")
	}

	reporters = []Reporter{
		&TelegramReporter{
			TelegramToken:      Config.TelegramToken,
			TelegramChat:       Config.TelegramChat,
			TelegramConfigPath: Config.TelegramConfigPath,
			Config:             &Config,
		},
		&SlackReporter{
			SlackToken: Config.SlackToken,
			SlackChat:  Config.SlackChat,
		},
	}

	for _, reporter := range reporters {
		reporter.Init()

		if reporter.Enabled() {
			log.Info().Str("name", reporter.Name()).Msg("Init reporter")
		}
	}

	for {
		report := GenerateReport()
		if report == nil || len(report.Entries) == 0 {
			log.Info().Msg("Report is empty, not sending.")
			time.Sleep(time.Duration(Config.Interval) * time.Second)
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

		time.Sleep(time.Duration(Config.Interval) * time.Second)
	}
}

func GenerateReport() *Report {
	newState, err := GetNewState()
	if err != nil {
		log.Error().Err(err).Msg("Error getting new state")
		return &Report{}
	}

	if len(State) == 0 {
		log.Info().Msg("No previous state, skipping.")
		State = newState
		return &Report{}
	}

	entries := []ReportEntry{}

	for address, info := range newState {
		oldState, ok := State[address]
		if !ok {
			log.Warn().Str("address", address).Msg("No old state present for address")
			continue
		}

		entry, present := GetValidatorReportEntry(oldState, info)
		if !present {
			log.Trace().
				Str("address", address).
				Msg("No report entry present")
			continue
		}

		entries = append(entries, *entry)
	}

	State = newState

	return &Report{Entries: entries}
}

func GetNewState() (ValidatorsState, error) {
	log.Debug().Msg("Querying for signing infos...")

	slashingClient := slashingtypes.NewQueryClient(grpcConn)
	signingInfos, err := slashingClient.SigningInfos(
		context.Background(),
		&slashingtypes.QuerySigningInfosRequest{
			Pagination: &querytypes.PageRequest{
				Limit: Config.Limit,
			},
		},
	)
	if err != nil {
		log.Error().Err(err).Msg("Could not query for signing info")
		return nil, err
	}

	stakingClient := stakingtypes.NewQueryClient(grpcConn)
	validatorsResult, err := stakingClient.Validators(
		context.Background(),
		&stakingtypes.QueryValidatorsRequest{
			Pagination: &querytypes.PageRequest{
				Limit: Config.Limit,
			},
		},
	)
	if err != nil {
		log.Error().Err(err).Msg("Could not query for validators")
		return nil, err
	}

	validatorsMap := make(map[string]stakingtypes.Validator, len(validatorsResult.Validators))
	for _, validator := range validatorsResult.Validators {
		err := validator.UnpackInterfaces(interfaceRegistry)
		if err != nil {
			log.Error().Err(err).Msg("Could not unpack interface")
			return nil, err
		}

		pubKey, err := validator.GetConsAddr()
		if err != nil {
			log.Error().Err(err).Msg("Could not get cons addr")
			return nil, err
		}

		validatorsMap[pubKey.String()] = validator
	}

	newState := make(ValidatorsState, len(signingInfos.Info))

	for _, info := range signingInfos.Info {
		validator, ok := validatorsMap[info.Address]
		if !ok {
			log.Warn().Str("address", info.Address).Msg("Could not find validator by pubkey")
			continue
		}

		if !IsValidatorMonitored(validator.OperatorAddress) {
			log.Trace().Str("address", info.Address).Msg("Not monitoring this validator, skipping.")
			continue
		}

		newState[info.Address] = ValidatorState{
			Address:          validator.OperatorAddress,
			Moniker:          validator.Description.Moniker,
			ConsensusAddress: info.Address,
			MissedBlocks:     info.MissedBlocksCounter,
			Jailed:           validator.Jailed,
			Tombstoned:       info.Tombstoned,
		}
	}

	return newState, nil
}

func GetValidatorReportEntry(oldState, newState ValidatorState) (*ReportEntry, bool) {
	log.Trace().
		Str("oldState", fmt.Sprintf("%+v", oldState)).
		Str("newState", fmt.Sprintf("%+v", newState)).
		Msg("Processing validator report entry")

	// 1. If validator's tombstoned, but wasn't - set tombstoned report entry.
	if newState.Tombstoned && !oldState.Tombstoned {
		log.Debug().
			Str("address", oldState.Address).
			Msg("Validator is tombstoned")
		return &ReportEntry{
			ValidatorAddress: newState.Address,
			ValidatorMoniker: newState.Moniker,
			Emoji:            TombstonedEmoji,
			Description:      TombstonedDesc,
			Direction:        TOMBSTONED,
		}, true
	}

	// 2. If validator's jailed, but wasn't - set jailed report entry.
	if newState.Jailed && !oldState.Jailed {
		log.Debug().
			Str("address", oldState.Address).
			Msg("Validator is jailed")
		return &ReportEntry{
			ValidatorAddress: newState.Address,
			ValidatorMoniker: newState.Moniker,
			Emoji:            JailedEmoju,
			Description:      JailedDesc,
			Direction:        JAILED,
		}, true
	}

	// 3. If validator's not jailed, but was - set unjailed report entry.
	if !newState.Jailed && oldState.Jailed {
		log.Debug().
			Str("address", oldState.Address).
			Msg("Validator is unjailed")
		return &ReportEntry{
			ValidatorAddress: newState.Address,
			ValidatorMoniker: newState.Moniker,
			Emoji:            UnjailedEmoji,
			Description:      UnjailedDesc,
			Direction:        UNJAILED,
		}, true
	}

	// 4. If validator is and was jailed - do nothing.
	if newState.Jailed && oldState.Jailed {
		log.Debug().
			Str("address", oldState.Address).
			Msg("Validator is and was jailed - no need to send report")
		return nil, false
	}

	// 5. Validator isn't and wasn't jailed.
	//
	// First, check if old and new groups are the same - if they have different start,
	// they are different. If they don't - they aren't so no need to send a notification.
	oldGroup, oldGroupErr := Config.MissedBlocksGroups.GetGroup(oldState.MissedBlocks)
	if oldGroupErr != nil {
		log.Error().Err(oldGroupErr).Msg("Could not get old group")
		return nil, false
	}
	newGroup, newGroupErr := Config.MissedBlocksGroups.GetGroup(newState.MissedBlocks)
	if newGroupErr != nil {
		log.Error().Err(newGroupErr).Msg("Could not get new group")
		return nil, false
	}

	if oldGroup.Start == newGroup.Start {
		log.Debug().
			Str("address", oldState.Address).
			Int64("before", oldState.MissedBlocks).
			Int64("after", newState.MissedBlocks).
			Msg("Validator didn't change group - no need to send report")
		return nil, false
	}

	// Validator switched from one MissedBlockGroup to another, 2 cases how that may happen
	// 1) validator is skipping blocks
	// 2) validator skipped some blocks in the past, but recovered, is now signing, and the window
	// moves - the amount of missed blocks is decreasing.
	// Need to understand which one it is: if old missed blocks < new missed blocks -
	// it's 1), if vice versa, then 2)

	entry := &ReportEntry{
		ValidatorAddress: newState.Address,
		ValidatorMoniker: newState.Moniker,
		MissingBlocks:    newState.MissedBlocks,
		Emoji:            newGroup.EmojiStart,
		Description:      newGroup.DescStart,
	}

	if oldState.MissedBlocks < newState.MissedBlocks {
		// skipping blocks
		log.Debug().
			Str("address", oldState.Address).
			Int64("before", oldState.MissedBlocks).
			Int64("after", newState.MissedBlocks).
			Msg("Validator's missed blocks increasing")
		entry.Direction = INCREASING
	} else {
		// restoring
		log.Debug().
			Str("address", oldState.Address).
			Int64("before", oldState.MissedBlocks).
			Int64("after", newState.MissedBlocks).
			Msg("Validator's missed blocks decreasing")
		entry.Direction = DECREASING
	}

	return entry, true
}

func SetBechPrefixes(cmd *cobra.Command) {
	if flag, err := cmd.Flags().GetString("bech-validator-prefix"); flag != "" && err == nil {
		Config.ValidatorPrefix = flag
	} else if Config.Prefix == "" {
		log.Fatal().Msg("Both bech-validator-prefix and bech-prefix are not set!")
	} else {
		Config.ValidatorPrefix = Config.Prefix + "valoper"
	}

	if flag, err := cmd.Flags().GetString("bech-validator-pubkey-prefix"); flag != "" && err == nil {
		Config.ValidatorPubkeyPrefix = flag
	} else if Config.Prefix == "" {
		log.Fatal().Msg("Both bech-validator-pubkey-prefix and bech-prefix are not set!")
	} else {
		Config.ValidatorPubkeyPrefix = Config.Prefix + "valoperpub"
	}

	if flag, err := cmd.Flags().GetString("bech-consensus-node-prefix"); flag != "" && err == nil {
		Config.ConsensusNodePrefix = flag
	} else if Config.Prefix == "" {
		log.Fatal().Msg("Both bech-consensus-node-prefix and bech-prefix are not set!")
	} else {
		Config.ConsensusNodePrefix = Config.Prefix + "valcons"
	}

	if flag, err := cmd.Flags().GetString("bech-consensus-node-pubkey-prefix"); flag != "" && err == nil {
		Config.ConsensusNodePubkeyPrefix = flag
	} else if Config.Prefix == "" {
		log.Fatal().Msg("Both bech-consensus-node-pubkey-prefix and bech-prefix are not set!")
	} else {
		Config.ConsensusNodePubkeyPrefix = Config.Prefix + "valconspub"
	}
}

func IsValidatorMonitored(address string) bool {
	// If no args passed, we want to be notified about all validators.
	if len(Config.IncludeValidators) == 0 && len(Config.ExcludeValidators) == 0 {
		return true
	}

	// If monitoring only specific validators
	if len(Config.IncludeValidators) != 0 {
		for _, monitoredValidatorAddr := range Config.IncludeValidators {
			if monitoredValidatorAddr == address {
				return true
			}
		}

		return false
	}

	// If monitoring all validators except the specified ones
	for _, monitoredValidatorAddr := range Config.ExcludeValidators {
		if monitoredValidatorAddr == address {
			return false
		}
	}

	return true
}

func SetMissedBlocksToJail() {
	slashingClient := slashingtypes.NewQueryClient(grpcConn)
	params, err := slashingClient.Params(
		context.Background(),
		&slashingtypes.QueryParamsRequest{},
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not query for slashing params")
	}

	// because cosmos's dec doesn't have .toFloat64() method or whatever and returns everything as int
	minSignedPerWindow, err := strconv.ParseFloat(params.Params.MinSignedPerWindow.String(), 64)
	if err != nil {
		log.Fatal().
			Err(err).
			Msg("Could not parse delegator shares")
	}

	SignedBlocksWindow = params.Params.SignedBlocksWindow
	MissedBlocksToJail = int64(float64(params.Params.SignedBlocksWindow) * (1 - minSignedPerWindow))

	log.Info().
		Int64("missedBlocksToJail", MissedBlocksToJail).
		Msg("Missed blocks to jail calculated")
}

func SetAvgBlockTime() {
	latestBlock := GetBlock(nil)
	latestHeight := latestBlock.Height
	beforeLatestBlockHeight := latestBlock.Height - BlocksDiffInThePast
	beforeLatestBlock := GetBlock(&beforeLatestBlockHeight)

	heightDiff := float64(latestHeight - beforeLatestBlockHeight)
	timeDiff := latestBlock.Time.Sub(beforeLatestBlock.Time).Seconds()

	AvgBlockTime = timeDiff / heightDiff

	log.Info().
		Float64("heightDiff", heightDiff).
		Float64("timeDiff", timeDiff).
		Float64("avgBlockTime", AvgBlockTime).
		Msg("Average block time calculated")
}

func SetDefaultMissedBlocksGroups() {
	if Config.MissedBlocksGroups != nil {
		log.Debug().Msg("MissedBlockGroups is set, not setting the default ones.")
		return
	}

	totalRange := float64(MissedBlocksToJail) + 1 // from 0 till max blocks allowed, including

	groups := []MissedBlocksGroup{}

	percents := []float64{0, 0.5, 1, 5, 10, 25, 50, 75, 90, 100}
	emojiStart := []string{"🟡", "🟡", "🟡", "🟠", "🟠", "🟠", "🔴", "🔴", "🔴"}
	emojiEnd := []string{"🟢", "🟡", "🟡", "🟡", "🟡", "🟠", "🟠", "🟠", "🟠"}

	for i := 0; i < len(percents)-1; i++ {
		start := totalRange * percents[i] / 100
		end := totalRange*percents[i+1]/100 - 1

		groups = append(groups, MissedBlocksGroup{
			Start:      int64(start),
			End:        int64(end),
			EmojiStart: emojiStart[i],
			EmojiEnd:   emojiEnd[i],
			DescStart:  fmt.Sprintf("is skipping blocks (> %.1f%%)", percents[i]),
			DescEnd:    fmt.Sprintf("is recovering (< %.1f%%)", percents[i+1]),
		})
	}

	Config.MissedBlocksGroups = groups
}

func GetBlock(height *int64) *ctypes.Block {
	client, err := tmrpc.New(Config.TendermintRPC, "/websocket")
	if err != nil {
		log.Fatal().Err(err).Msg("Could not create Tendermint client")
	}

	block, err := client.Block(context.Background(), height)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not query Tendermint status")
	}

	return block.Block
}

func main() {
	rootCmd.PersistentFlags().StringVar(&Config.ConfigPath, "config", "", "Config file path")
	rootCmd.PersistentFlags().StringVar(&Config.NodeAddress, "node", "localhost:9090", "RPC node address")
	rootCmd.PersistentFlags().StringVar(&Config.LogLevel, "log-level", "info", "Logging level")
	rootCmd.PersistentFlags().BoolVar(&Config.JsonOutput, "json", false, "Output logs as JSON")
	rootCmd.PersistentFlags().IntVar(&Config.Interval, "interval", 120, "Interval between two checks, in seconds")
	rootCmd.PersistentFlags().Uint64Var(&Config.Limit, "limit", 1000, "gRPC query pagination limit")
	rootCmd.PersistentFlags().StringVar(&Config.MintscanPrefix, "mintscan-prefix", "", "Prefix for mintscan links like https://mintscan.io/{prefix}")
	rootCmd.PersistentFlags().StringVar(&Config.TendermintRPC, "tendermint-rpc", "http://localhost:26657", "Tendermint RPC address")

	rootCmd.PersistentFlags().StringVar(&Config.TelegramToken, "telegram-token", "", "Telegram bot token")
	rootCmd.PersistentFlags().IntVar(&Config.TelegramChat, "telegram-chat", 0, "Telegram chat or user ID")
	rootCmd.PersistentFlags().StringVar(&Config.TelegramConfigPath, "telegram-config", "", "Telegram config path")
	rootCmd.PersistentFlags().StringVar(&Config.SlackToken, "slack-token", "", "Slack bot token")
	rootCmd.PersistentFlags().StringVar(&Config.SlackChat, "slack-chat", "", "Slack chat or user ID")

	rootCmd.PersistentFlags().StringSliceVar(&Config.IncludeValidators, "include", []string{}, "Validators to monitor")
	rootCmd.PersistentFlags().StringSliceVar(&Config.ExcludeValidators, "exclude", []string{}, "Validators to not monitor")

	// some networks, like Iris, have the different prefixes for address, validator and consensus node
	rootCmd.PersistentFlags().StringVar(&Config.Prefix, "bech-prefix", "", "Bech32 global prefix")
	rootCmd.PersistentFlags().StringVar(&Config.ValidatorPrefix, "bech-validator-prefix", "", "Bech32 validator prefix")
	rootCmd.PersistentFlags().StringVar(&Config.ValidatorPubkeyPrefix, "bech-validator-pubkey-prefix", "", "Bech32 pubkey validator prefix")
	rootCmd.PersistentFlags().StringVar(&Config.ConsensusNodePrefix, "bech-consensus-node-prefix", "", "Bech32 consensus node prefix")
	rootCmd.PersistentFlags().StringVar(&Config.ConsensusNodePubkeyPrefix, "bech-consensus-node-pubkey-prefix", "", "Bech32 pubkey consensus node prefix")

	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	if err := rootCmd.Execute(); err != nil {
		log.Fatal().Err(err).Msg("Could not start application")
	}
}
