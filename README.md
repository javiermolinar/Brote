<p align="center">
  <img src="assets/brote-plant.png" width="180" alt="Brote, a friendly carnivorous plant that catches bugs">
</p>
<h1 align="center">Brote</h1>
<p align="center"><strong>Your LLM debugger companion.</strong></p>
<p align="center">Pause the program. Ask beside the code. Figure it out together.</p>

Brote brings your debugger and your agent into the same conversation. See the
actual stack and variables, ask why a value looks wrong, and keep the answer
attached to the code and the pause that prompted it.

Brote uses the **Debug Adapter Protocol (DAP)**. The currently supported language
is **Go**, through **Delve**; other language backends are not included yet.

Use the browser inspector with **Codex or Pi**, or stay in **VS Code** with native
debugger comments and Chat. You can inspect and set breakpoints together. The
agent only steps or continues when you authorize it.

![Brote paused in a Go program, with the call stack, locals, and a threaded agent conversation](assets/debugger-conversation.png)

*An example conversation in the browser inspector: explain a surprising value,
then ask where to break next. The program stays paused throughout.*

## Install

Each integration includes the precompiled core and browser inspector—no clone
or build needed. Release checksums are verified automatically. Shared Go sessions
support **macOS and Linux, arm64 and x64**, and require
[Delve](https://github.com/go-delve/delve).

The first public release is pending. These commands will work once it is published.

### Codex
```sh
curl -fsSL https://github.com/javiermolinar/Brote/releases/latest/download/install.sh | sh -s -- --agent codex
```
### Pi
```sh
pi install git:github.com/javiermolinar/Brote
```
### VS Code
```sh
curl -fsSL https://github.com/javiermolinar/Brote/releases/latest/download/install.sh | sh -s -- --editor vscode
```
Start your normal debugger with **F5**, pause, and choose **Ask Brote About
Selection**. Pick an available model when prompted; answers appear in native
comment threads. Use **Continue in Chat** for a longer conversation, or `@brote`
to start a shared session from Chat.

## Try it

Open your Go project and start Pi:

```sh
cd mycoolproject
pi
```

Then ask:

> Let's debug this project with Brote. Start a session, stop at `main`, and
> give me the debugger URL. Leave it paused so I can explore.

Open the URL to see your code, call stack, and variables. Select a line or click
a variable and ask:

> Where does this value come from?

The reply appears beside the code. Step through the program and keep asking
follow-up questions in the same thread.

[Debugging guide](docs/debugging.md)

## How it fits together

```text
Codex / Pi ─── tools + events ───┐
                               │
Browser inspector ─── HTTP ─── Brote core ─── DAP ─── Delve ─── Go program
                               │
VS Code extension ──────────────┘
        └── also works with your existing F5 debug session
```

The Go core coordinates sessions, bounded agent execution, and durable history.
A typed client connects the integrations over a local HTTP API and SSE events.
The browser UI is embedded in the native binary; running a release needs no Go
or Node build tools.

The local API listens on `127.0.0.1` without bearer authentication. It is intended
for a trusted local machine, not network exposure. Saved debugger evidence can
contain application data.

MIT licensed. Third-party notices are retained.
