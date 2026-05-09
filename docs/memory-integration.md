# AgencyCli Memory Integration

AgencyCli can inject small, retrieved memory snippets into agent prompts from
OpenViking and EverCore/EverMemOS. The integration is off by default so normal
runs do not spend extra tokens or embedding/API calls.

## Enable

Set these environment variables globally, per agent, or in the shell that starts
AgencyCli:

```bash
AGENCYCLI_MEMORY_ENABLED=1
AGENCYCLI_MEMORY_MAX_SNIPPETS=5
AGENCYCLI_MEMORY_MAX_CHARS=6000

AGENCYCLI_OPENVIKING_URL=http://127.0.0.1:1933
AGENCYCLI_OPENVIKING_API_KEY=...
AGENCYCLI_OPENVIKING_ACCOUNT=default
AGENCYCLI_OPENVIKING_USER=default

AGENCYCLI_EVERCORE_URL=http://127.0.0.1:1995
AGENCYCLI_EVERCORE_USER_ID=agencycli
AGENCYCLI_EVERCORE_METHOD=keyword
AGENCYCLI_EVERCORE_MEMORY_TYPES=episodic_memory,agent_memory
```

## Cost Controls

- `AGENCYCLI_MEMORY_ENABLED` defaults to off.
- `AGENCYCLI_EVERCORE_METHOD=keyword` is the default because it avoids vector
  embedding calls for EverCore retrieval.
- `AGENCYCLI_MEMORY_MAX_SNIPPETS` and `AGENCYCLI_MEMORY_MAX_CHARS` cap prompt
  growth before the agent CLI is invoked.
- If either memory service is unavailable, AgencyCli logs a warning in the run
  log and continues without memory context.
