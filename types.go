package main

import (
	"fmt"
	"time"
)

type Direction int

const (
	INCREASING = iota
	DECREASING
	JAILED
	UNJAILED
	TOMBSTONED
)

const (
	TombstonedEmoji = "💀"
	JailedEmoju     = "❌"
	UnjailedEmoji   = "👌"
)

const (
	TombstonedDesc = "was tombstoned"
	JailedDesc     = "was jailed"
	UnjailedDesc   = "was unjailed"
)

type AppConfig struct {
	ConfigPath     string
	NodeAddress    string
	LogLevel       string
	JsonOutput     bool
	Interval       int
	Limit          uint64
	MintscanPrefix string
	TendermintRPC  string

	TelegramToken      string
	TelegramConfigPath string
	TelegramChat       int
	SlackToken         string
	SlackChat          string

	Prefix                    string
	ValidatorPrefix           string
	ValidatorPubkeyPrefix     string
	ConsensusNodePrefix       string
	ConsensusNodePubkeyPrefix string

	IncludeValidators []string
	ExcludeValidators []string
}

type ValidatorState struct {
	Address          string
	Moniker          string
	ConsensusAddress string
	MissedBlocks     int64
	Jailed           bool
	Tombstoned       bool
}

type ValidatorsState map[string]ValidatorState

type MissedBlocksGroup struct {
	Start      int64
	End        int64
	EmojiStart string
	EmojiEnd   string
	DescStart  string
	DescEnd    string
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

type ReportEntry struct {
	ValidatorAddress string
	ValidatorMoniker string
	Emoji            string
	Description      string
	MissingBlocks    int64
	Direction        Direction
}

func (r ReportEntry) GetTimeToJail() time.Duration {
	blocksLeftToJail := MissedBlocksToJail - r.MissingBlocks
	secondsLeftToJail := AvgBlockTime * float64(blocksLeftToJail)

	return time.Duration(secondsLeftToJail) * time.Second
}

type Report struct {
	Entries []ReportEntry
}

type Reporter interface {
	Serialize(Report) string
	Init()
	Enabled() bool
	SendReport(Report) error
	Name() string
}
