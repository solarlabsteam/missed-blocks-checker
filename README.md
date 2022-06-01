# missed-blocks-checker

![Latest release](https://img.shields.io/github/v/release/solarlabsteam/missed-blocks-checker)
[![Actions Status](https://github.com/solarlabsteam/missed-blocks-checker/workflows/test/badge.svg)](https://github.com/solarlabsteam/missed-blocks-checker/actions)

missed-blocks-checker is a tool that sends a message to configured channels if any of the Cosmos validators starts or stops missing blocks. It queries the data from the gRPC endpoint.

## How can I set it up?

Download the latest release from [the releases page](https://github.com/solarlabsteam/missed-blocks-checker/releases/). After that, you should unzip it and you are ready to go:

```sh
wget <the link from the releases page>
tar <downloaded file>
./missed-blocks-checker --telegram-token <bot token> --telegram-chat <user or chat ID from the previous step>
```

Alternatively, install `golang` (>1.18), clone the repo and build it. This will generate a `./main` binary file in the repository folder:
```
git clone https://github.com/solarlabsteam/missed-blocks-checker
cd missed-blocks-checker
go build
```

What you probably want to do is to have it running in the background. For that, first of all, we have to copy the file to the system apps folder:

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
ExecStart=missed-blocks-checker --config <config path>
Restart=always
RestartSec=2
LimitNOFILE=800000
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
```

Then we'll add this service to the autostart and run it:

```sh
sudo systemctl daemon-reload # reload config to reflect changed
sudo systemctl enable missed-blocks-checker # put service to autostart
sudo systemctl start missed-blocks-checker # start the service
sudo systemctl status missed-blocks-checker # validate it's running
```

If you need to, you can also see the logs of the process:

```sh
sudo journalctl -u missed-blocks-checker -f --output cat
```

## How does it work?

It periodically queries the full node via gRPC for all validators and their missed blocks, then checks the difference with the missed blocks before and now. If the validator is faulty, it writes a Telegram message to a specified chat.

## How can I configure it?

All configuration is done via `.toml` config file, which is mandatory. Run the app with `--config <path/to/config.toml>` to specify config. Check out `config.example.toml` to see the params that can be set.

## Notifications channels

Currently this program supports the following notifications channels:
1) Telegram

Go to @BotFather in Telegram and create a bot. After that, there are two options:
- you want to send messages to a user. This user should write a message to @getmyid_bot, then copy the `Your user ID` number. Also keep in mind that the bot won't be able to send messages unless you contact it first, so write a message to a bot before proceeding.
- you want to send messages to a channel. Write something to a channel, then forward it to @getmyid_bot and copy the `Forwarded from chat` number. Then add the bot as an admin.


Then add a Telegram config to your config file (see `config.example.toml` for reference).

2) Slack

Go to the Slack web interface -> Manage apps and create a new app.
Give the app the `chat:write` scope and add the integration to a channel by typing `/invite <bot username>` there.
After that add a Slack config to your config file (see `config.example.toml` for reference).


## Which networks this is guaranteed to work?

In theory, it should work on a Cosmos-based blockchains that expose a gRPC endpoint.

## How can I contribute?

Bug reports and feature requests are always welcome! If you want to contribute, feel free to open issues or PRs.
