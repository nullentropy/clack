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

clack acts as you through a slack app that you create in your workspace. Everything the
app needs (its user scopes, the events it listens for and Socket Mode) is in
[`slack-manifest.json`](slack-manifest.json), so making one takes a few clicks.

1. Start clack and select **Create the Slack app** on the connect screen. Or go to
   <https://api.slack.com/apps>, select **Create New App**, then **From a manifest**, and
   paste in `slack-manifest.json`.
2. Choose the workspace and create the app.
3. Select **Install to Workspace**, then copy the **User OAuth Token** (`xoxp-...`) from
   **OAuth & Permissions**.
4. Under **Basic Information**, generate an **App-Level Token** with the
   `connections:write` scope (`xapp-...`). This is the one step a manifest can't do.

To change what the app can do later, edit its manifest under **App Manifest** in the app's
settings and reinstall it.

The first time clack starts without tokens, it asks for both and saves them in
`~/.config/clack/settings.json` (or under `$XDG_CONFIG_HOME`). If slack rejects them
or doesn't answer within 30 seconds then the client will ask again.
