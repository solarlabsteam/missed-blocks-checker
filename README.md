# missed-blocks-checker

![Latest release](https://img.shields.io/github/v/release/solarlabsteam/missed-blocks-checker)
[![Actions Status](https://github.com/solarlabsteam/missed-blocks-checker/workflows/test/badge.svg)](https://github.com/solarlabsteam/missed-blocks-checker/actions)

missed-blocks-checker is a tool that sends a message to configured channels if any of the Cosmos validators starts or stops missing blocks. It queries the data from the gRPC endpoint.

## How can I set it up?

Download the latest release from [the releases page](https://github.com/solarlabsteam/missed-blocks-checker/releases/). After that, you should unzip it and you are ready to go:

```sh
wget <the link from the releases page>
tar xvfz missed-blocks-checker_*
./missed-blocks-checker --telegram-token <bot token> --telegram-chat <user or chat ID from the previous step>
```

That's not really interesting, what you probably want to do is to have it running in the background. For that, first of all, we have to copy the file to the system apps folder:

```sh
sudo cp ./missed-blocks-checker /usr/bin
```

Then we need to create a systemd service for our app:

```sh
sudo nano /etc/systemd/system/missed-blocks-checker.service
```

You can use this template (change the user to whatever user you want this to be executed from. It's advised to create a separate user for that instead of running it from root):

```
[Unit]
Description=Missed Blocks Checker
After=network-online.target

[Service]
User=<username>
TimeoutStartSec=0
CPUWeight=95
IOWeight=95
ExecStart=missed-blocks-checker --telegram-token <bot token> --telegram-chat <user or chat ID>
Restart=always
RestartSec=2
LimitNOFILE=800000
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
```

Then we'll add this service to the autostart and run it:

```sh
sudo systemctl daemon-reload
sudo systemctl enable missed-blocks-checker
sudo systemctl start missed-blocks-checker
sudo systemctl status missed-blocks-checker # validate it's running
```

If you need to, you can also see the logs of the process:

```sh
sudo journalctl -u missed-blocks-checker -f --output cat
```

## How does it work?

It periodically queries the full node via gRPC for all validators and their missed blocks, then checks the difference with the missed blocks before and now. If the validator is faulty, it writes a Telegram message to a specified chat.

## How can I configure it?

You can pass the artuments to the executable file to configure it. Here is the parameters list:

- `--bech-prefix` - the global prefix for addresses. Defaults to `persistence`
- `--node` - the gRPC node URL. Defaults to `localhost:9090`
- `--log-devel` - logger level. Defaults to `info`. You can set it to `debug` to make it more verbose.
- `--limit` - pagination limit for gRPC requests. Defaults to 1000.
- `--telegram-token` - Telegram bot token
- `--telegram-chat` - Telegram user or chat ID
- `--threshold` - If the missed blocks count is below this value, the messages are not sent. Defaults to 0.
- `--mintscan-prefix` - This bot generates links to Mintscan for validators, using this prefix. Links have the following format: `https://mintscan.io/<mintscan-prefix>/validator/<validator ID>`. Defaults to `persistence`.
- `--interval` - Interval between the two checks, in seconds. Defaults to 120
- `--include` - a comma-separated list of validators' operators addresses. If specified, only the validators from this list would be monitored.
- `--exclude` - a comma-separated list of validators' operators addresses. If specified, all validators except the ones from this list would be monitored.

(Note that you cannot use `--include` and `--exclude` at the same time.)


You can also specify custom Bech32 prefixes for wallets, validators, consensus nodes, and their pubkeys by using the following params:
- `--bech-validator-prefix`
- `--bech-validator-pubkey-prefix`
- `--bech-consensus-node-prefix`
- `--bech-consensus-node-pubkey-prefix`

By default, if not specified, it defaults to the next values (as it works this way for the most of the networks):
- `--bech-validator-prefix`  = `--bech-prefix` + "valoper"
- `--bech-validator-pubkey-prefix` = `--bech-prefix` + "valoperpub"
- `--bech-consensus-node-prefix` = `--bech-prefix` + "valcons"
- `--bech-consensus-node-pubkey-prefix` = `--bech-prefix` + "valconspub"

An example of the network where you have to specify all the prefixes manually is Iris.

Additionally, you can pass a `--config` flag with a path to your config file (I use .toml, but anything supported by [viper](https://github.com/spf13/viper) should work).

## Notifications channels

Currently this program supports the following notifications channels:
1) Telegram

Go to @BotFather in Telegram and create a bot. After that, there are two options:
- you want to send messages to a user. This user should write a message to @getmyid_bot, then copy the `Your user ID` number. Also keep in mind that the bot won't be able to send messages unless you contact it first, so write a message to a bot before proceeding.
- you want to send messages to a channel. Write something to a channel, then forward it to @getmyid_bot and copy the `Forwarded from chat` number. Then add the bot as an admin.


Then run a program with `--telegram-token <token> --telegram-chat <chat ID>`.

2) Slack

Go to the Slack web interface -> Manage apps and create a new app.
Give the app the `chat:write` scope and add the integration to a channel by typing `/invite <bot username>` there.
After that, run the program with `--slack-token <token> --slack-chat <channel name>`.


## Which networks this is guaranteed to work?

In theory, it should work on a Cosmos-based blockchains that expose a gRPC endpoint.

## How can I contribute?

Bug reports and feature requests are always welcome! If you want to contribute, feel free to open issues or PRs.
