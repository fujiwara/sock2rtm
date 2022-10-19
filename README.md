# sock2rtm

SlackのSocketModeで受信したmessage eventをRTMのように配信するWebSocketサーバー(proxy)です。

```
sock2rtm -h
Usage of ./sock2rtm:
  -debug
        debug mode
  -port int
        port number (default 8888)
  -version
        show version
```

環境変数 `SLACK_BOT_TOKEN` と `SLACK_APP_TOKEN` が必要です。

## API

### `/start/{channel_id_1},{channel_id_2}...`

Slackの`/api/rtm.start`(廃止済み)を模したレスポンスを返します。

URL pathに指定したチャンネルID(`,`区切りで複数指定可能)にいるmemberの情報を取得して、レスポンスの`users`に含めます。

```console
$ curl http://localhost:8888/start/C7MK19D7F,C0ATSF2MF
{
  "ok": true,
  "url": "ws://localhost:8888/websocket/C7MK19D7F,C0ATSF2MF", // WebSocket接続用URL
  "users": [{...}, {...},....], // ユーザー情報
}
```

- Perlの[AnyEvent::SlackRTM](https://metacpan.org/pod/AnyEvent::SlackRTM) を使用する場合、`$AnyEvent::SlackRTM::START_URL` をこのAPIのURLに書き変えてください
- bot自身はチャンネルへのjoinは行いません。必要なチャンネルへinviteしてください

### `/websocket/{channel_id_1},{channel_id_2}...`

SlackのRTMのようにwebsocketでメッセージを配信します。

- 配信されるのはmessage eventのみです
- (botがjoinしているチャンネルのうち)URLで指定したチャンネルのメッセージのみ配信されます


## LICENSE

MIT
