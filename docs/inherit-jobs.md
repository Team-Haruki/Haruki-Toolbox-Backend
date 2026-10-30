# Inherit jobs and the inherit vault: API contract

This is the contract between the backend and the Toolbox frontend for uploading game data with a transfer code (引继码). It replaces `POST /api/inherit/{server}/{upload_type}/submit`, which now answers `410`. Delete this document once the frontend has shipped; after that the code and its comments are the reference.

## 1. Why the flow changed

Project SEKAI JP (and EN, expected to follow) changed transfer codes. A code is valid for 24 hours after it is generated and works once. A second executing call returns a bare `403` with no body. After another device executes the transfer, the device that generated the code can no longer log in. The old route executed the transfer, fetched the data and dropped the session, so the owner was left without a way back into the account.

The new flow:

- requires a Toolbox login;
- shows the account before anything is used up, and waits for the owner to confirm;
- stores the game login credential in an encrypted vault (7 days), right after the transfer and before anything else;
- issues a new transfer code right after login and before fetching data, and shows it to the owner;
- lets the owner see that code again, or issue another one from the stored credential, from the vault.

## 2. Conventions

- **Authentication.** Every endpoint below needs the Kratos session cookie (the browser sends it; Oathkeeper checks it). Without one: `401`.
- **Envelope.** Responses use the usual envelope:

  ```json
  {"status": 200, "message": "ok", "updatedData": { ... }}
  ```

  Errors carry a stable machine code in `updatedData.code` and a Chinese message for display in `message`:

  ```json
  {"status": 400, "message": "引继码格式不正确（应为 16 位字母或数字）", "updatedData": {"code": "inherit_id_invalid"}}
  ```

  Branch on `code`, never on `message`.
- **Ownership.** A job or vault entry that belongs to someone else is reported exactly like one that does not exist (`404`).
- **Times** are RFC 3339 UTC strings.
- **Game user IDs** are only ever shown masked (`16*************363`).

## 3. Steps

A job goes through these steps in order. Every step is listed in every snapshot from the start, with a status.

| Step | What happens | Can it consume the code? |
| --- | --- | --- |
| `bootstrap` | Signed cookies (JP) and current app version | no |
| `preview` | Non-executing inherit call (`isExecuteInherit=False`): nickname, rank, masked game user ID, transfer restriction | no |
| `awaiting_confirm` | The job pauses until the owner confirms (5 minutes at most) | no |
| `execute` | Executing inherit call; the game hands back the account credential | **yes**, from here on |
| `vault_store_credential` | The credential is sealed and committed to the vault before any further network call | |
| `login` | Login with the credential | |
| `issue_new_code` | New transfer code (retried 3 times) | |
| `vault_store_code` | The new code is sealed into the same vault entry | |
| `fetch_suite` | Suite data, with the client's usual follow-up calls | |
| `fetch_mysekai` | MySekai data (only for `upload_type: mysekai`, else `skipped`) | |
| `upload` | Data is stored like any inherit upload | |
| `done` | Finished successfully | |

Step status: `pending`, `running`, `done`, `failed`, `skipped`. A step may list sub-steps (one per group of game requests; a sub-step reported twice updates its entry) with status `running`, `done`, `warning` (a non-critical call failed, the step goes on) or `failed`:

| Step | Sub-steps |
| --- | --- |
| `bootstrap` | `cookies` (JP only), `version` |
| `preview` | `inherit_check` |
| `execute` | `inherit_execute` |
| `login` | `auth` |
| `issue_new_code` | `issue_code` (the same entry is updated on each of the up to 3 attempts) |
| `fetch_suite` | `suite`, `suite_followup`, `home_refresh`, `home_refresh_final` |
| `fetch_mysekai` | `maintenance_check`, `mysekai`, `mysekai_followup` |

Job status: `running`, `awaiting_confirm`, `succeeded`, `failed`, `cancelled`.

## 4. Job endpoints

### 4.1 Create a job

`POST /api/inherit/jobs`

```json
{
  "server": "jp",
  "upload_type": "suite",
  "inherit_id": "ABCDEFGH12345678",
  "inherit_password": "the code's password",
  "new_password_mode": "reuse"
}
```

| Field | Rule |
| --- | --- |
| `server` | `jp` or `en` |
| `upload_type` | `suite` or `mysekai`. `mysekai` needs a verified QQ binding |
| `inherit_id` | 16 letters or digits (surrounding spaces are trimmed) |
| `inherit_password` | required, at most 64 bytes; not trimmed |
| `new_password_mode` | `reuse`: the new code gets the password just entered, which must then be 8–16 printable ASCII characters without spaces. `random`: 12 random characters from `[A-Za-z0-9]` |

Everything above is checked before the game server is contacted.

`202`:

```json
{"status": 202, "message": "job created", "updatedData": {"job_id": "…uuid…", "job": { /* snapshot, §4.2 */ }}}
```

Errors: `invalid_request`, `invalid_server`, `invalid_upload_type`, `inherit_id_invalid`, `inherit_password_invalid`, `new_password_mode_invalid`, `reuse_password_invalid` (400); `mysekai_requires_qq` (403); `job_already_running` (409, one active job per user; see below); `rate_limited` (429, 10 jobs per user per hour); `server_busy` (429, `Retry-After`); `game_degraded` (503, `Retry-After`); `vault_unavailable`, `identity_unavailable`, `store_unavailable` (503).

`job_already_running` also carries the ID of the caller's own running job, so a page that lost it (reload, another tab) can reattach with §4.2 and the WebSocket instead of waiting for the job to end. The ID is only ever the caller's own job; when the backend cannot name it, `job_id` is absent.

```json
{"status": 409, "message": "你已有一个进行中的引继任务", "updatedData": {"code": "job_already_running", "job_id": "…uuid…"}}
```

### 4.2 Snapshot

`GET /api/inherit/jobs/{job_id}` returns the job (`200`). The record lives for one hour after creation.

```json
{
  "id": "…",
  "server": "jp",
  "upload_type": "suite",
  "new_password_mode": "reuse",
  "status": "awaiting_confirm",
  "current_step": "awaiting_confirm",
  "steps": [
    {"name": "bootstrap", "status": "done", "started_at": "…", "finished_at": "…",
     "substeps": [{"name": "cookies", "status": "done", "at": "…"}, {"name": "version", "status": "done", "at": "…"}]},
    {"name": "preview", "status": "done", "started_at": "…", "finished_at": "…", "substeps": [ … ]},
    {"name": "awaiting_confirm", "status": "running", "started_at": "…"},
    {"name": "execute", "status": "pending"}
  ],
  "preview": {
    "nickname": "Miku",
    "rank": 150,
    "game_user_id_masked": "16*************363",
    "transfer_restriction": {
      "is_restrict_device_transfer": false,
      "restrict_end_at": 0,
      "rest_transfer_count": 0,
      "is_world_bloom_chapter": false,
      "restrict_rank": 0,
      "game_character_id": 0
    }
  },
  "unlock": {"kind": "password", "initialized": false},
  "confirm_deadline": "…",
  "error": {"code": "…", "message": "…"},
  "warnings": [{"code": "…", "message": "…"}],
  "vault_entry_id": "…",
  "new_code": {"inherit_id": "NEWCODE987654321", "password": "…", "expires_at": "…"},
  "created_at": "…",
  "updated_at": "…",
  "finished_at": "…"
}
```

- `transfer_restriction` mirrors the game's `UserRestrictInfo`; `restrict_end_at` is in epoch milliseconds. It is absent when the game sends none. If `is_restrict_device_transfer` is true and `rest_transfer_count` is 0, the job fails with `transfer_restricted` instead of waiting.
- `unlock` says what the confirm step needs (§6): `kind` is `password` (the Toolbox password) or `pin` (a vault PIN). `initialized: false` means the owner's vault key does not exist yet and the confirm input creates it.
- `new_code` appears as soon as `issue_new_code` succeeded and stays for the life of the job record (one hour), **whatever happens afterwards**: a later failure still shows it. After that, the code is only available from the vault (§5).
- `error` is set when the status is `failed` or `cancelled`; `warnings` lists problems that did not stop the job (§7).
- `vault_entry_id` appears once the credential is stored.

### 4.3 Confirm

`POST /api/inherit/jobs/{job_id}/confirm`, only while `status` is `awaiting_confirm`:

```json
{"password": "toolbox password"}
```

or, for `unlock.kind: pin`, `{"vault_pin": "…"}`. See §6 for `previous_password`. `202` with the snapshot. The job then executes the transfer.

Errors: `job_not_found` (404); `job_not_awaiting_confirm` (409); every unlock error of §6.

### 4.4 Cancel

`POST /api/inherit/jobs/{job_id}/cancel`, allowed until the owner confirms. Nothing has been executed, so the code stays valid. `200` with the snapshot; the job ends as `cancelled` / `cancelled`. After confirmation: `409 cannot_cancel`.

### 4.5 WebSocket

1. `POST /api/inherit/jobs/{job_id}/ws-ticket` → `{"ticket": "…", "expires_in": 30}`. The ticket works once, for this job and this user, within 30 seconds.
2. Open `wss://<api host>/api/inherit/jobs/{job_id}/ws?ticket=<ticket>` (the session cookie is sent as usual). A bad or used ticket gets `401 ws_ticket_invalid` before the upgrade.

Server to client (JSON text frames):

| `type` | Fields | When |
| --- | --- | --- |
| `snapshot` | `job` | Right after connecting |
| `event` | `event`, `job` | Every step or sub-step transition, and every change of the job record. `job` is the full new snapshot; `event` is `{"step", "substep"?, "status", "note"?, "error_code"?, "at"}` |
| `ack` | `action` (`confirm` / `cancel`), `job` | The command was accepted |
| `error` | `action`, `code`, `message` | The command was refused (same codes as the HTTP endpoints) |
| `ping` | `at` | Every 15 seconds |

The server also sends WebSocket ping control frames every 15 seconds (browsers answer them by themselves) and closes the socket when nothing has arrived for 60 seconds. When the job ends, the last `event` carries the final snapshot and the server closes with code 1000. A job that is already finished gets one `snapshot` and a close.

Client to server:

```json
{"type": "confirm", "password": "…"}
{"type": "confirm", "vault_pin": "…"}
{"type": "confirm", "password": "…", "previous_password": "…"}
{"type": "cancel"}
{"type": "pong"}
```

If the socket drops, fetch a new ticket and reconnect; the first `snapshot` brings the client up to date. `GET` (§4.2) works as a polling fallback.

## 5. Vault endpoints

A vault entry is one transferred account: its login credential (kept 7 days from the transfer) and the latest transfer code issued for it (24 hours from issue). Both are encrypted to the owner's key (§8).

### 5.1 Key status

`GET /api/inherit/vault/key`:

```json
{"initialized": true, "kind": "password", "key_version": 1793375683278469, "created_at": "…", "updated_at": "…"}
{"initialized": false, "kind": "pin", "identity_has_password": false}
```

`key_version` identifies the key pair. It is an opaque, increasing integer (below 2^53) that changes on every reset; entries sealed to an earlier version answer `entry_stale`.

### 5.2 List entries

`GET /api/inherit/vault/entries` → `200` with an array, newest first (metadata only):

```json
[{
  "id": "…",
  "server": "jp",
  "game_user_id_masked": "16*************363",
  "job_id": "…",
  "created_at": "…",
  "credential_expires_at": "…",
  "has_code": true,
  "code_issued_at": "…",
  "code_expires_at": "…",
  "readable": true,
  "pending": false
}]
```

`has_code` is false once the code is older than 24 hours (or none was issued). `readable` is false for entries sealed to a key that was reset. `pending` is true while the entry is temporarily parked in Redis because PostgreSQL was unavailable; it still opens normally.

### 5.3 Reveal the current code

`POST /api/inherit/vault/entries/{entry_id}/reveal` with the unlock body (§6) → `{"inherit_id", "password", "code_expires_at"}`.

Errors: `entry_not_found` (404), `code_not_available` (404: no code or older than 24 hours; re-issue instead), `entry_stale` (410), unlock errors.

### 5.4 Issue a new code

`POST /api/inherit/vault/entries/{entry_id}/reissue`:

```json
{"password": "toolbox password", "new_password_mode": "random"}
```

The backend logs in with the stored credential and issues a new code, which replaces the stored one; the game invalidates the previous code. This takes a few seconds. `200`:

```json
{"inherit_id": "…", "password": "…", "code_expires_at": "…", "warnings": [{"code": "vault_store_failed", "message": "…"}]}
```

`reuse` reuses the password of the stored code; without one: `409 no_previous_password`. If the new code could not be stored, it is still returned once, with a `vault_store_failed` or `vault_db_unavailable` warning: show it prominently.

Errors: `entry_not_found`, `entry_stale`, `no_previous_password`, `game_degraded`, `server_busy`, `reissue_login_failed` (502: the stored credential no longer works), `reissue_rejected`, `reissue_failed` (502), unlock errors.

### 5.5 Destroy an entry ("我已恢复")

`DELETE /api/inherit/vault/entries/{entry_id}` → `200`. The entry becomes unreadable at once and is deleted shortly after. No unlock needed.

### 5.6 Reset the vault

`POST /api/inherit/vault/key/reset`:

```json
{"password": "toolbox password", "confirm": true}
```

Discards the key pair and **destroys every entry**. Identities without a Toolbox password send only `{"confirm": true}`. `200` → `{"destroyed_entries": 2}`. Errors: `reset_confirmation_required` (400), `password_invalid`, `unlock_rate_limited`.

## 6. Unlock (step-up)

Confirming a job, revealing and re-issuing all carry the owner's secret in the body. It is checked on every request; there is no "recently re-authenticated" window, because the same input is what decrypts the owner's key.

| `unlock.kind` | Send | Checked by |
| --- | --- | --- |
| `password` | `password`: the Toolbox password | Kratos (a real password login) |
| `pin` | `vault_pin`: 8–128 characters | opening the key |

- **First use.** The first job confirmation creates the key pair from the same input: the password for identities that have one, otherwise a new vault PIN the owner chooses then. Ask for the PIN twice in the UI; it cannot be recovered.
- **Identities without a password.** Toolbox accounts created through Google or Apple sign-in, or with a passkey only, have no Toolbox password; they use a vault PIN. Such an account that later adds a password keeps its PIN for the vault.
- **Password changed.** Toolbox passwords are changed in the Kratos settings page, which the backend never sees. After a change, the new password passes Kratos but does not open the key: the request fails with `409 vault_password_changed`. Ask for the previous password once and repeat the request with both:

  ```json
  {"password": "new password", "previous_password": "old password"}
  ```

  The key is then protected by the new password from that request on.
- **Password reset (forgotten password).** After a reset through account recovery the previous password is unknown, so the stored entries cannot be opened any more. The same `vault_password_changed` answer appears; the only way forward is the vault reset (§5.6), which destroys the entries. The message says so; show the reset as the second option of that dialog. A forgotten vault PIN works the same way.
- **Attempts.** At most 10 unlock attempts per user per 15 minutes; then `429 unlock_rate_limited`. A success clears the count.

Unlock errors: `password_required`, `vault_pin_required`, `vault_pin_invalid_format` (400); `password_invalid`, `vault_pin_invalid`, `previous_password_invalid` (403); `vault_password_changed`, `vault_not_initialized` (409); `unlock_rate_limited` (429); `identity_unavailable`, `vault_unavailable`, `store_unavailable` (503).

## 7. Job error and warning codes

`job.error.code` (the job stopped):

| Code | Meaning | Code still valid? |
| --- | --- | --- |
| `inherit_invalid` | 继承码无效、已使用或已过期 (the game answered 403) | — |
| `inherit_restricted` | The game refuses transfers for this account (405) | yes |
| `transfer_restricted` | No transfers left during the current event | yes |
| `confirm_timeout` | Not confirmed within 5 minutes | yes |
| `cancelled` | Cancelled by the owner | yes |
| `game_degraded`, `server_busy` | Refused before executing | yes |
| `game_maintenance`, `game_unavailable`, `game_error` | Before `execute`: nothing used. During fetch: see below | |
| `execute_uncertain` | The executing call failed in transit: the code may have been used. Tell the owner not to retry it elsewhere and to check the vault | unknown |
| `login_failed` | The credential is in the vault but login failed. Offer re-issue from the vault | no |
| `fetch_failed`, `mysekai_unavailable` | Data could not be fetched. The new code (if issued) is in the snapshot and the vault | no |
| `upload_failed`, `upload_forbidden` | The data could not be stored (`upload_forbidden`: the account is bound to another Toolbox user) | no |
| `job_interrupted` | The backend restarted during the job | depends on the last finished step |
| `internal_error` | | |

`job.warnings[].code` (the job went on):

| Code | Meaning | What to show |
| --- | --- | --- |
| `issue_code_failed` | No new code was issued; the credential is in the vault | Ask the owner to re-issue from the vault now: it is their only way back into the account |
| `vault_db_unavailable` | PostgreSQL was down; the sealed credential is parked in Redis and moved over later | Informational |
| `vault_store_failed` | The credential (or the new code) could not be stored at all | Insist that the owner writes down `new_code` now |

## 8. Storage, retention and security

- **Encryption.** Each owner has an X25519 key pair. The private key is wrapped with a key derived from the owner's Toolbox password (or vault PIN) with Argon2id, then with a server master key. Vault records are sealed to the public key (ephemeral X25519, HKDF-SHA256, AES-256-GCM), bound to the entry ID, owner, server and key version. The database alone, the master key alone, or the owner's password alone cannot open a record. The format records `wrap_scheme` (`x25519-argon2id-v1` for keys, `x25519-hkdf-sha256-aes256gcm-v1` for entries), the Argon2id parameters and the key and master-key versions, so any of them can change later.
- **During a job** the confirm step uses only the public key; the private key is opened in memory for that request and dropped. The new code shown in the snapshot is kept, for the job record's hour, sealed under the master key alone and bound to the job and owner. This is the one place the server could read a code without the owner; it is limited to the newest code and one hour, and the 7-day credential is never stored that way.
- **Retention.** The credential is deleted 7 days after the transfer, the code 24 hours after it was issued, a destroyed entry within minutes. A reset deletes everything.
- **Audit and notices.** Job creation, confirmation and cancellation, credential storage, reveal, re-issue, destroy, key creation, key re-wrap and reset are recorded in the system log without secrets or full game user IDs. A reveal or re-issue also sends an email to a verified address.
- **Frontend.** Do not keep the transfer code or its password in `localStorage` (the old "remember" option did); the job only needs them in the create request.

## 9. The old endpoint

`POST /api/inherit/{server}/{upload_type}/submit` answers `410` for every server and type, with `updatedData.code: "client_update_required"` and a message telling the user to reload the page or update the client.
