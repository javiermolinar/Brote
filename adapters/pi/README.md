# Pi debugger commands

After installing or updating the adapter, run `/reload` in Pi.

- `/debug-sessions` lists current sessions, their projects, statuses, and browser URLs. Ended sessions are omitted; offline sessions remain visible.
- `/debug-connect SESSION_ID` connects this conversation and displays the inspector URL.
- `/debug-stop SESSION_ID` terminates that debugger session and its target process. Saved history is retained. An unavailable broker must be recovered before stopping the session.

The agent can use `debug_sessions`, `debug_connect`, and `debug_stop` for equivalent natural-language requests. Stopping requires an explicit user request for the named session.
