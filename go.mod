module main

go 1.16

replace github.com/gogo/protobuf => github.com/regen-network/protobuf v1.3.3-alpha.regen.1

require (
	github.com/cosmos/cosmos-sdk v0.42.4
	github.com/rs/zerolog v1.21.0
	google.golang.org/grpc v1.37.0
	gopkg.in/tucnak/telebot.v2 v2.3.5
)
