# ACTION_MODE safety gating

`ACTION_MODE` (`dry_run` / `label_only` / `auto_trash`, defined in `internal/config`) controls how aggressively the agent may mutate the user's mailbox. The invariant: **destructive capability is gated in tool code, never only in the agent's instruction prompt.** The LLM cannot be trusted to self-restrict.

When adding or changing tools in `internal/tools/`:

- Pass the mode into the tool constructor (see `gmailtools.Tools(client, mode)`) and gate behavior there.
- In `dry_run`, a tool must log what it *would* do and return a result, without performing any external write.
- Tools that delete or trash anything must not be registered at all outside `auto_trash` mode (`gmail_trash` is the existing example).
- Never widen the Gmail OAuth scope beyond `gmail.modify`; permanent deletion must stay impossible.
- New mutating tools should support idempotency where the API allows it (e.g. `calendar_create_event` takes `srcMessageId` to dedupe).

## Additional trigger surfaces

The same agent (with the same `ACTION_MODE`-gated tools) can now be invoked from `internal/slackbot` in addition to the ADK REST API and the cron batch. Any new trigger surface into the agent must not bypass or duplicate the gating above — gating stays in the tool layer regardless of who/what called the agent.

Because Slack input reaches the mailbox/calendar-mutating tools directly, `internal/slackbot` restricts callers to `SLACK_ALLOWED_USER_ID` when set. Don't remove this check or make it default to "allow all"; anyone who can `@mention` the bot in a shared channel would otherwise be able to drive Gmail/Calendar writes under the owner's credentials.
