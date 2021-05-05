package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/cosmos/cosmos-sdk/simapp"
	sdk "github.com/cosmos/cosmos-sdk/types"

	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/rs/zerolog"
	telegramBot "gopkg.in/tucnak/telebot.v2"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	ConfigPath     string
	NodeAddress    string
	LogLevel       string
	TelegramToken  string
	TelegramChat   int
	Interval       int
	Threshold      int64
	Limit          uint64
	MintscanPrefix string

	Prefix                    string
	ValidatorPrefix           string
	ValidatorPubkeyPrefix     string
	ConsensusNodePrefix       string
	ConsensusNodePubkeyPrefix string
)

var log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout}).With().Timestamp().Logger()

var beforePreviousInfos []slashingtypes.ValidatorSigningInfo
var previousInfos []slashingtypes.ValidatorSigningInfo

var validators []stakingtypes.Validator

var bot *telegramBot.Bot

var encCfg = simapp.MakeTestEncodingConfig()
var interfaceRegistry = encCfg.InterfaceRegistry

var validatorsToMonitor []string

var rootCmd = &cobra.Command{
	Use:  "missed-blocks-checker",
	Long: "Tool to monitor missed blocks for Cosmos-chain validators",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if ConfigPath == "" {
			log.Trace().Msg("No config file provided, skipping")
			return nil
		}

		log.Trace().Msg("Config file provided")

		viper.SetConfigFile(ConfigPath)
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

		return nil
	},
	Run: Execute,
}

func Execute(cmd *cobra.Command, args []string) {
	logLevel, err := zerolog.ParseLevel(LogLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not parse log level")
	}

	zerolog.SetGlobalLevel(logLevel)

	validatorsToMonitor = args

	config := sdk.GetConfig()
	config.SetBech32PrefixForValidator(ValidatorPrefix, ValidatorPubkeyPrefix)
	config.SetBech32PrefixForConsensusNode(ConsensusNodePrefix, ConsensusNodePubkeyPrefix)
	config.Seal()

	log.Info().
		Str("--node", NodeAddress).
		Str("--log-level", LogLevel).
		Int("--interval", Interval).
		Int64("--threshold", Threshold).
		Uint64("--limit", Limit).
		Str("--bech-validator-prefix", ValidatorPrefix).
		Str("--bech-validator-pubkey-prefix", ValidatorPubkeyPrefix).
		Str("--bech-consensus-node-prefix", ConsensusNodePrefix).
		Str("--bech-consensus-node-pubkey-prefix", ConsensusNodePubkeyPrefix).
		Msg("Started with following parameters")

	if len(validatorsToMonitor) == 0 {
		log.Info().Msg("Monitoring all validators")
	} else {
		log.Info().
			Strs("validators", validatorsToMonitor).
			Msg("Monitoring specific validators")
	}

	bot, err = telegramBot.NewBot(telegramBot.Settings{
		Token:  TelegramToken,
		Poller: &telegramBot.LongPoller{Timeout: 10 * time.Second},
	})

	if err != nil {
		log.Fatal().Err(err).Msg("Could not create Telegram bot")
	}

	grpcConn, err := grpc.Dial(
		NodeAddress,
		grpc.WithInsecure(),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not establish gRPC connection")
	}

	defer grpcConn.Close()

	for {
		checkValidators(grpcConn)
		time.Sleep(time.Duration(Interval) * time.Second)
	}
}

func checkValidators(grpcConn *grpc.ClientConn) {
	log.Trace().Msg("=============== Request start =================")
	defer log.Trace().Msg("=============== Request end =================")

	slashingClient := slashingtypes.NewQueryClient(grpcConn)
	signingInfos, err := slashingClient.SigningInfos(
		context.Background(),
		&slashingtypes.QuerySigningInfosRequest{
			Pagination: &querytypes.PageRequest{
				Limit: Limit,
			},
		},
	)

	if err != nil {
		log.Error().Err(err).Msg("Could not query for signing info")
		return
	}

	stakingClient := stakingtypes.NewQueryClient(grpcConn)
	validatorsResult, err := stakingClient.Validators(
		context.Background(),
		&stakingtypes.QueryValidatorsRequest{
			Pagination: &querytypes.PageRequest{
				Limit: Limit,
			},
		},
	)

	if err != nil {
		log.Error().Err(err).Msg("Could not query for validators")
		return
	}

	validators = validatorsResult.Validators

	log.Trace().Msg("Validators list:")
	for _, signingInfo := range signingInfos.Info {
		log.Trace().
			Str("address", signingInfo.Address).
			Int64("startHeight", signingInfo.StartHeight).
			Int64("missedBlocks", signingInfo.MissedBlocksCounter).
			Msg("-- Validator info")
	}

	if previousInfos == nil {
		log.Info().Msg("Previous infos is empty, first start. No checking difference")
		previousInfos = signingInfos.Info
		return
	}

	var sb strings.Builder

	missedBlocksIncreased := 0
	missedBlocksDecreased := 0
	missedBlocksNotChanged := 0
	missedBlocksBelowThreshold := 0

	log.Debug().Msg("Processing validators")
	for _, signingInfo := range signingInfos.Info {
		log.Debug().Str("pubkey", signingInfo.Address).Msg("-- Validator info")
		var previousInfo slashingtypes.ValidatorSigningInfo
		previousInfoFound := false
		for _, previousInfoIterated := range previousInfos {
			if previousInfoIterated.Address == signingInfo.Address {
				previousInfo = previousInfoIterated
				previousInfoFound = true
				break
			}
		}

		if !previousInfoFound {
			log.Debug().Str("address", signingInfo.Address).Msg("---- Could not find previous info")
			continue
		}

		// if it's zero - validator hasn't missed any blocks since last check
		// if it's > 0 - validator has missed some blocks since last check
		// if it's < 0 - validator has missed some blocks in the past
		// but the window is moving and they are not missing blocks now.
		previous := previousInfo.MissedBlocksCounter
		current := signingInfo.MissedBlocksCounter
		diff := current - previous

		var validatorLink string

		// somehow not all the validators info is returned
		validator, found := findValidator(signingInfo.Address)
		if found {
			log.Debug().Str("address", validator.OperatorAddress).Msg("---- Found validator for pubkey")

			if !isValidatorMonitored(validator.OperatorAddress) {
				log.Debug().Msg("---- Monitoring specific validators - skipping.")
				continue
			}

			validatorLink = fmt.Sprintf(
				"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
				MintscanPrefix,
				validator.OperatorAddress,
				validator.Description.Moniker,
			)
		} else {
			// if monitoring all validators, we want to be notified also about
			// those where we cannot get the validator info, if specific ones - we want
			// to skip these
			if len(validatorsToMonitor) != 0 {
				log.Debug().Msg("---- No pubkey info, monitoring specific validators - skipping.")
				continue
			}

			log.Debug().Str("address", signingInfo.Address).Msg("---- Could not find validator for pubkey")
			validatorLink = fmt.Sprintf("validator with key <pre>%s</pre>", signingInfo.Address)
		}

		if current <= Threshold && previous <= Threshold {
			missedBlocksBelowThreshold += 1
			continue
		}

		log.Debug().
			Str("address", signingInfo.Address).
			Int64("missedBlocks", diff).
			Int64("before", previous).
			Int64("after", current).
			Msg("---- Validator diff with previous state")

		var emoji string
		var status string

		// Possible cases:
		// 1) previous state < threshold, current state < threshold - validator is not missing blocks, ignoring
		// 2) previous state < threshold, current state > threshold - validator started missing blocks
		// 3) previous state > threshold, current state > threshold, diff > 0 - validator is still missing blocks
		// 4) previous state > threshold, current state > threshold, diff == 0 - validator stopped missing blocks
		// 5) previous state > threshold, current state > threshold, diff < 0 - window is moving
		// 6) previous state > threshold, current state < threshold - window moved, validator is back to normal

		if current > Threshold && previous <= Threshold {
			// 2
			emoji = "🚨"
			status = "is missing blocks"
			log.Debug().Msg("---- Validator started missing blocks")
			missedBlocksIncreased += 1
		} else if current > Threshold && previous > Threshold && diff > 0 {
			// 3
			emoji = "🔴"
			status = "is missing blocks"
			log.Debug().Msg("---- Validator is still missing blocks")
			missedBlocksIncreased += 1
		} else if current > Threshold && previous > Threshold && diff == 0 {
			// 4
			// This is where it gets crazy: we need to check not the previous state,
			// but the one before it, to see if we've sent any notifications redarding that.
			log.Debug().Msg("---- Validator stopped missing blocks")
			missedBlocksNotChanged += 1

			var beforePreviousInfo slashingtypes.ValidatorSigningInfo
			beforePreviousInfoFound := false
			for _, beforePreviousInfoIterated := range beforePreviousInfos {
				if beforePreviousInfoIterated.Address == signingInfo.Address {
					beforePreviousInfo = beforePreviousInfoIterated
					beforePreviousInfoFound = true
					break
				}
			}

			if !beforePreviousInfoFound {
				log.Debug().Msg("---- Could not find before previous info")
				continue
			}

			// Now, if current diff is zero, but diff between previous and before previous is above zero,
			// that means we haven't sent a notification so far, and should do it.
			// If previous diff is negative, that means the window has moved, and we won't need to notify.
			// If previous diff is zero, everything is stable, no need to send notifications as well.
			previousDiff := previousInfo.MissedBlocksCounter - beforePreviousInfo.MissedBlocksCounter
			if previousDiff == 0 {
				log.Debug().Msg("---- Previous diff == 0, notification already sent.")
				continue
			} else if previousDiff < 0 {
				log.Debug().Msg("---- Previous diff < 0, not sending notification.")
				continue
			} else {
				log.Debug().Msg("---- Previous diff > 0, sending notification.")
			}

			emoji = "🟡"
			status = "stopped missing blocks"
		} else if current > Threshold && previous > Threshold && diff < 0 {
			// 5
			log.Debug().Msg("---- Window is moving, diff is negative")
			missedBlocksDecreased += 1
			continue
		} else if current <= Threshold && previous > Threshold && diff < 0 {
			missedBlocksDecreased += 1
			// 6
			emoji = "🟢"
			status = "went back to normal"
		} else {
			log.Fatal().Msg("Unexpected state")
		}

		sb.WriteString(fmt.Sprintf(
			"%s <strong>%s %s</strong>: %d -> %d\n\n",
			emoji,
			validatorLink,
			status,
			previousInfo.MissedBlocksCounter,
			signingInfo.MissedBlocksCounter,
		))

	}

	log.Info().
		Int("missedBlocksIncreased", missedBlocksIncreased).
		Int("missedBlocksNotChanged", missedBlocksNotChanged).
		Int("missedBlocksDecreased", missedBlocksDecreased).
		Int("missedBlocksBelowThreshold", missedBlocksBelowThreshold).
		Msg("Validators diff")

	tgMessage := sb.String()

	if tgMessage != "" {
		log.Debug().Str("msg", sb.String()).Msg("Formatted string")
		_, err = bot.Send(&telegramBot.User{ID: TelegramChat}, sb.String(), telegramBot.ModeHTML)
		if err != nil {
			log.Error().
				Err(err).
				Msg("Could not send Telegram message")
			return
		}
	}

	beforePreviousInfos = previousInfos
	previousInfos = signingInfos.Info
}

func findValidator(address string) (stakingtypes.Validator, bool) {
	for _, validatorIterated := range validators {
		err := validatorIterated.UnpackInterfaces(interfaceRegistry)
		if err != nil {
			// shouldn't happen
			log.Error().Err(err).Msg("Could not unpack interface")
			return stakingtypes.Validator{}, false
		}

		pubKey, err := validatorIterated.GetConsAddr()
		if err != nil {
			log.Error().
				Str("address", validatorIterated.OperatorAddress).
				Err(err).
				Msg("Could not get validator pubkey")
		}

		if pubKey.String() == address {
			return validatorIterated, true
		}
	}

	return stakingtypes.Validator{}, false
}

func isValidatorMonitored(address string) bool {
	// If no args passed, we want to be notified about all validators.
	if len(validatorsToMonitor) == 0 {
		return true
	}

	for _, monitoredValidatorAddr := range validatorsToMonitor {
		if monitoredValidatorAddr == address {
			return true
		}
	}

	return false
}

func main() {
	rootCmd.PersistentFlags().StringVar(&ConfigPath, "config", "", "Config file path")
	rootCmd.PersistentFlags().StringVar(&NodeAddress, "node", "localhost:9090", "RPC node address")
	rootCmd.PersistentFlags().StringVar(&LogLevel, "log-level", "info", "Logging level")
	rootCmd.PersistentFlags().StringVar(&TelegramToken, "telegram-token", "", "Telegram bot token")
	rootCmd.PersistentFlags().IntVar(&TelegramChat, "telegram-chat", 0, "Telegram chat or user ID")
	rootCmd.PersistentFlags().IntVar(&Interval, "interval", 120, "Interval between two checks, in seconds")
	rootCmd.PersistentFlags().Int64Var(&Threshold, "threshold", 0, "Threshold of missed blocks")
	rootCmd.PersistentFlags().Uint64Var(&Limit, "limit", 1000, "gRPC query pagination limit")
	rootCmd.PersistentFlags().StringVar(&MintscanPrefix, "mintscan-prefix", "persistence", "Prefix for mintscan links like https://mintscan.io/{prefix}")

	// some networks, like Iris, have the different prefixes for address, validator and consensus node
	rootCmd.PersistentFlags().StringVar(&Prefix, "bech-prefix", "persistence", "Bech32 global prefix")
	rootCmd.PersistentFlags().StringVar(&ValidatorPrefix, "bech-validator-prefix", Prefix+"valoper", "Bech32 validator prefix")
	rootCmd.PersistentFlags().StringVar(&ValidatorPubkeyPrefix, "bech-validator-pubkey-prefix", Prefix+"valoperpub", "Bech32 pubkey validator prefix")
	rootCmd.PersistentFlags().StringVar(&ConsensusNodePrefix, "bech-consensus-node-prefix", Prefix+"valcons", "Bech32 consensus node prefix")
	rootCmd.PersistentFlags().StringVar(&ConsensusNodePubkeyPrefix, "bech-consensus-node-pubkey-prefix", Prefix+"valconspub", "Bech32 pubkey consensus node prefix")

	if err := rootCmd.MarkPersistentFlagRequired("telegram-token"); err != nil {
		log.Fatal().Err(err).Msg("Could not mark flag as required")
	}

	if err := rootCmd.MarkPersistentFlagRequired("telegram-chat"); err != nil {
		log.Fatal().Err(err).Msg("Could not mark flag as required")
	}

	if err := rootCmd.Execute(); err != nil {
		log.Fatal().Err(err).Msg("Could not start application")
	}
}
