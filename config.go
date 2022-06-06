package main

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/mcuadros/go-defaults"
)

type TelegramAppConfig struct {
	Token      string `toml:"token"`
	Chat       int    `toml:"chat"`
	ConfigPath string `toml:"config-path"`
}

type SlackConfig struct {
	Token string `toml:"token"`
	Chat  string `toml:"chat"`
}

type LogConfig struct {
	LogLevel   string `toml:"level" default:"info"`
	JSONOutput bool   `toml:"json" default:"false"`
}

type ChainInfoConfig struct {
	MintscanPrefix       string `toml:"mintscan-prefix"`
	ValidatorPagePattern string `toml:"validator-page-pattern"`
}

func (c *ChainInfoConfig) GetValidatorPage(address string, text string) string {
	// non-mintscan links
	if c.ValidatorPagePattern != "" {
		href := fmt.Sprintf(c.ValidatorPagePattern, address)
		return fmt.Sprintf("<a href=\"%s\">%s</a>", href, text)
	}

	return fmt.Sprintf(
		"<a href=\"https://www.mintscan.io/%s/validators/%s\">%s</a>",
		c.MintscanPrefix,
		address,
		text,
	)
}

type NodeConfig struct {
	GrpcAddress   string `toml:"grpc-address" default:"localhost:9090"`
	TendermintRPC string `toml:"rpc-address" default:"http://localhost:26657"`
}

type AppConfig struct {
	LogConfig       LogConfig       `toml:"log"`
	ChainInfoConfig ChainInfoConfig `toml:"chain-info"`
	NodeConfig      NodeConfig      `toml:"node"`

	Interval int `toml:"interval" default:"120"`

	Prefix                    string `toml:"bech-prefix"`
	ValidatorPrefix           string `toml:"bech-validator-prefix"`
	ValidatorPubkeyPrefix     string `toml:"bech-validator-pubkey-prefix"`
	ConsensusNodePrefix       string `toml:"bech-consensus-node-prefix"`
	ConsensusNodePubkeyPrefix string `toml:"bech-consensus-node-pubkey-prefix"`

	IncludeValidators []string `toml:"include-validators"`
	ExcludeValidators []string `toml:"exclude-validators"`

	MissedBlocksGroups MissedBlocksGroups `toml:"missed-blocks-groups"`

	TelegramConfig TelegramAppConfig `toml:"telegram"`
	SlackConfig    SlackConfig       `toml:"slack"`
}

type MissedBlocksGroup struct {
	Start      int64  `toml:"start"`
	End        int64  `toml:"end"`
	EmojiStart string `toml:"emoji-start"`
	EmojiEnd   string `toml:"emoji-end"`
	DescStart  string `toml:"desc-start"`
	DescEnd    string `toml:"desc-end"`
}

type MissedBlocksGroups []MissedBlocksGroup

// Checks that MissedBlocksGroup is an array of sorted MissedBlocksGroup
// covering each interval.
// Example (start - end), given that window = 300:
// 0 - 99, 100 - 199, 200 - 300 - valid
// 0 - 50 - not valid.
func (g MissedBlocksGroups) Validate(window int64) error {
	if len(g) == 0 {
		return fmt.Errorf("MissedBlocksGroups is empty")
	}

	if g[0].Start != 0 {
		return fmt.Errorf("first MissedBlocksGroup's start should be 0, got %d", g[0].Start)
	}

	if g[len(g)-1].End < window {
		return fmt.Errorf("last MissedBlocksGroup's end should be >= %d, got %d", window, g[len(g)-1].End)
	}

	for i := 0; i < len(g)-1; i++ {
		if g[i+1].Start-g[i].End != 1 {
			return fmt.Errorf(
				"MissedBlocksGroup at index %d ends at %d, and the next one starts with %d",
				i,
				g[i].End,
				g[i+1].Start,
			)
		}
	}

	return nil
}

func (g MissedBlocksGroups) GetGroup(missed int64) (*MissedBlocksGroup, error) {
	for _, group := range g {
		if missed >= group.Start && missed <= group.End {
			return &group, nil
		}
	}

	return nil, fmt.Errorf("could not find a group for missed blocks counter = %d", missed)
}

func LoadConfig(path string) (*AppConfig, error) {
	configBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	configString := string(configBytes)

	configStruct := AppConfig{}
	if _, err = toml.Decode(configString, &configStruct); err != nil {
		return nil, err
	}

	defaults.SetDefaults(&configStruct)
	return &configStruct, nil
}

func (config *AppConfig) Validate() {
	if len(config.IncludeValidators) != 0 && len(config.ExcludeValidators) != 0 {
		GetDefaultLogger().Fatal().Msg("Cannot use --include and --exclude at the same time!")
	}
}

func (config *AppConfig) SetBechPrefixes() {
	if config.Prefix == "" && config.ValidatorPrefix == "" {
		GetDefaultLogger().Fatal().Msg("Both bech-validator-prefix and bech-prefix are not set!")
	} else if config.ValidatorPrefix == "" {
		config.ValidatorPrefix = config.Prefix + "valoper"
	}

	if config.Prefix == "" && config.ValidatorPubkeyPrefix == "" {
		GetDefaultLogger().Fatal().Msg("Both bech-validator-pubkey-prefix and bech-prefix are not set!")
	} else if config.ValidatorPubkeyPrefix == "" {
		config.ValidatorPubkeyPrefix = config.Prefix + "valoperpub"
	}

	if config.Prefix == "" && config.ConsensusNodePrefix == "" {
		GetDefaultLogger().Fatal().Msg("Both bech-consensus-node-prefix and bech-prefix are not set!")
	} else if config.ConsensusNodePrefix == "" {
		config.ConsensusNodePrefix = config.Prefix + "valcons"
	}

	if config.Prefix == "" && config.ConsensusNodePubkeyPrefix == "" {
		GetDefaultLogger().Fatal().Msg("Both bech-consensus-node-pubkey-prefix and bech-prefix are not set!")
	} else if config.ConsensusNodePubkeyPrefix == "" {
		config.ConsensusNodePubkeyPrefix = config.Prefix + "valconspub"
	}
}

func (config *AppConfig) SetDefaultMissedBlocksGroups(params Params) {
	if config.MissedBlocksGroups != nil {
		GetDefaultLogger().Debug().Msg("MissedBlockGroups is set, not setting the default ones.")
		return
	}

	totalRange := float64(params.SignedBlocksWindow) + 1 // from 0 till max blocks allowed, including

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

	groups[0].DescEnd = fmt.Sprintf("is recovered (< %.1f%%)", percents[1])

	config.MissedBlocksGroups = groups
}

func (config *AppConfig) IsValidatorMonitored(address string) bool {
	// If no args passed, we want to be notified about all validators.
	if len(config.IncludeValidators) == 0 && len(config.ExcludeValidators) == 0 {
		return true
	}

	// If monitoring only specific validators
	if len(config.IncludeValidators) != 0 {
		for _, monitoredValidatorAddr := range config.IncludeValidators {
			if monitoredValidatorAddr == address {
				return true
			}
		}

		return false
	}

	// If monitoring all validators except the specified ones
	for _, monitoredValidatorAddr := range config.ExcludeValidators {
		if monitoredValidatorAddr == address {
			return false
		}
	}

	return true
}
