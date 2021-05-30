package main

type Direction int

const (
	START_MISSING_BLOCKS Direction = iota
	MISSING_BLOCKS
	STOPPED_MISSING_BLOCKS
	WENT_BACK_TO_NORMAL
	JAILED
)

type ReportEntry struct {
	ValidatorAddress    string
	ValidatorMoniker    string
	Pubkey              string
	Direction           Direction
	BeforeBlocksMissing int64
	NowBlocksMissing    int64
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
