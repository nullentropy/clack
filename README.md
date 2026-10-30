# clack

A more chill slack desktop client built with [caution](https://github.com/nullentropy/caution).
It only shows things that you've /joined or where you've been mentioned and doesn't include
a bunch of things that to me make slack a bit over-stimulating.

```
go run . -native
```

To build a macOS app:

```
go build -o clack . && go run github.com/nullentropy/caution/cmd/appbundle -bin ./clack -name clack -icon icon.png -o dist/
codesign --force -s - dist/clack.app
```

The last step signs the app as `dev.caution.clack` so macOS lets it post notifications.

## Connecting to Slack

clack acts as you through a slack app that you create in your workspace.

1. Go to <https://api.slack.com/apps> and select **Create New App**, then **From scratch**.
2. Under **Socket Mode**, turn on Socket Mode. Create an app-level token with the
   `connections:write` scope. This is the app-level token (`xapp-...`).
3. Under **OAuth & Permissions**, add these **User Token Scopes**:
   `channels:read`, `channels:history`, `groups:read`, `groups:history`,
   `im:read`, `im:history`, `mpim:read`, `mpim:history`, `users:read`, `chat:write`, `files:read`,
   `channels:write`, `groups:write`, `im:write` and `mpim:write` (to mark what
   you've read, join channels, set topics and start direct messages),
   `users:write` (for `/away`), `search:read` (for `/search`),
   `reactions:read` and `reactions:write` (to show reactions and `/react`), and
   `files:write` (to upload files you drop on the window).
4. Under **Event Subscriptions**, turn events on. Under **Subscribe to events on
   behalf of users**, add `message.channels`, `message.groups`, `message.im`,
   `message.mpim`, `reaction_added` and `reaction_removed`.
5. Install the app to the workspace. Copy the **User OAuth Token** (`xoxp-...`).

The first time clack starts without tokens, it asks for both and saves them in
`~/.config/clack/settings.json` (or under `$XDG_CONFIG_HOME`). If slack rejects them
or doesn't answer within 30 seconds then the client will ask again.
