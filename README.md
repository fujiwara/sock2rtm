# sock2rtm

SlackのSocketModeで受信したmessage eventをRTMのように配信するWebSocketサーバー(proxy)です。

```
sock2rtm -h
Usage of sock2rtm:
  -debug
        debug mode
  -port int
        port number (default 8888)
  -version
        show version
```

- 環境変数 `SLACK_BOT_TOKEN` と `SLACK_APP_TOKEN` が必要です
- botを動かすSlack Appには以下のOAuth scopeが必要です
  - channels:history
  - channels:read
  - users:read

## API

### `/start/{channel_id_1},{channel_id_2}.../{client_id}`

Slackの`/api/rtm.start`(廃止済み)を模したレスポンスを返します。

URL pathに指定したチャンネルID(`,`区切りで複数指定可能)にいるmemberの情報を取得して、レスポンスの`users`に含めます。

URL pathにはクライアントの識別子(client_id)を付与できます。同一のclient_idを持った接続に対しては、メッセージは1つのクライアントにしか配信されません。

```console
$ curl http://localhost:8888/start/C7MK19D7F,C0ATSF2MF/my_client_id
{
  "ok": true,
  "url": "ws://localhost:8888/websocket/C7MK19D7F,C0ATSF2MF/my_client_id", // WebSocket接続用URL
  "users": [{...}, {...},....], // ユーザー情報
}
```

- Perlの[AnyEvent::SlackRTM](https://metacpan.org/pod/AnyEvent::SlackRTM) を使用する場合、`$AnyEvent::SlackRTM::START_URL` をこのAPIのURLに書き変えてください
- bot自身はチャンネルへのjoinは行いません。必要なチャンネルへinviteしてください

### `/websocket/{channel_id_1},{channel_id_2}.../{client_id}`

SlackのRTMのようにwebsocketでメッセージを配信します。

- 配信されるのはmessage eventのみです
- (botがjoinしているチャンネルのうち)URLで指定したチャンネルのメッセージのみ配信されます

### `/metrics`

メトリクスをJSON形式で出力します。

```json
{
  "slack": {
    "hello": 1,
    "connecting": 1,
    "connected": 1,
    "disconnect": 0
  },
  "websocket": {
    "total_connections": 9,
    "current_connections": 1
  },
  "messages": {
    "received_from_slack": 0,
    "delivered_to_websocket": 0,
    "unsupported_from_slack": 0,
    "write_errored_to_websocket": 0
  }
}
```

## LICENSE

MIT
