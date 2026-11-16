# 対応している native 記録

`agent-metrics collect` は、Claude Code、Codex、OpenCode V2 が保存した記録から、
返却された識別子で指定した **1 回の子実行** を読む。親・兄弟・前後の依頼は
含めず、明示的な子孫だけを再帰的に含める。時刻や作業ディレクトリでは関連付けない。

数値と境界の固定値は各製品の `expected.json`、採取条件は `capture.json` にある。

* [Claude Code](../testdata/claude-code/native/expected.json) / [capture.json](../testdata/claude-code/native/capture.json)
* [Codex](../testdata/codex/native/expected.json) / [capture.json](../testdata/codex/native/capture.json)
* [OpenCode V2](../testdata/opencode-v2/native/expected.json) / [capture.json](../testdata/opencode-v2/native/capture.json)

ここでの「確認済み」は、その匿名化済み fixture を CLI が再生できたことである。

## 確認の範囲

この変更の検証は、リポジトリ内の fixture を隔離したホームまたは明示パスへ置いて
CLI を実行した再生である。Claude Code 2.1.312、Codex 0.165.0、OpenCode 2.2.1 の
実バイナリを起動したり、利用者の標準保存先にある live 記録を読んだりはしていない。
live の標準保存先での接続確認は未実施であり、fixture 再生の成功をその代替にしない。

再生で確認したことは次のとおり。

* 返却 ID だけで、標準の `~/.claude/projects`、`~/.codex/sessions`、
  `~/.local/share/opencode/opencode.db` から r1 の数値と境界を読む。
* `--records`、製品設定、既定位置の優先順位。無効な上位指定、空の保存先、
  対象不在は別の診断になり、別の実行へフォールバックしない。
* 再利用時は実行 ID を返して停止する。指定した 1 回だけを集計し、過去分や兄弟を足さない。
* 実行中・中断・欠損した子孫でも、確定している self と経過時間を残す。
* 本文 sentinel を JSON と診断へ出さない。JSONL と SQLite の入力ハッシュを変えない。
  存在しない SQLite ファイルを新規作成しない。

未採取の fork、compaction、counter reset、同一実行中のモデル切替、
別の起動経路は対象外である。計測に必要な未知形式は診断し、0 や完全な結果にはしない。

## 保存先

優先順位は `--records`、製品設定、既定位置。上位が無効または空なら下位へ進まない。

| 製品 | 設定 | 既定 | `--records` |
| --- | --- | --- | --- |
| Claude Code | `CLAUDE_CONFIG_DIR/projects` | `~/.claude/projects` | projects ルート、または 1 つの transcript |
| Codex | `CODEX_HOME/sessions` | `~/.codex/sessions` | sessions ルート、または 1 つの rollout |
| OpenCode V2 | `OPENCODE_DB` | `~/.local/share/opencode/opencode.db` | DB ファイル 1 つ |

相対 `OPENCODE_DB` は `~/.local/share/opencode` 基準である。作業ディレクトリ基準にはしない。
採取版で未確認の XDG 配置や release channel へ対応範囲は広げない。
明示した transcript や DB に親や子が無い場合は、その範囲の不足を診断し、無指定の別ルートは探さない。

## Claude Code 2.1.312

採取は対話型 CLI（`entrypoint=cli`）。設定上書きは別プロセスの `claude -p` で、
起動前の `CLAUDE_CONFIG_DIR` に transcript が作られた経路だけを L2 とする。
根拠は [`testdata/claude-code/native/capture.json`](../testdata/claude-code/native/capture.json)。

* `--agent` は Agent 結果の `toolUseResult.agentId`。
* `--execution` は、開始 user 記録の `uuid`。初回は spawn prompt、再利用は
  `origin.kind=coordinator` の user。`promptId` では選ばない。
* 区間は開始 user から次の coordinator の直前まで。終端は区間内の最後の
  loop terminal（`stop_reason=end_turn`、`toolEndsTurn`、または
  `[Request interrupted by user]`）で、呼び出し側の停止通知
  （`queue-operation` の `completed`/`killed`、または foreground の
  `status=completed`）が対応するときだけ確定する。途中の `end_turn` や
  handback だけでは終わらせない。
* 子の `.meta.json` が無くても、親の launch 結果が子 transcript を指すときはその子を含める。参照だけで実行記録が無いときは `missing_descendant` のままにする。
* usage は assistant `message.id` ごとに 1 回。`message.id` が無い行は
  その記録の `uuid` で残し、`missing_message_id` で重複を確定できないことを
  診断する。完全な小計にはしない。`total` は
  input + cache creation + cache read + output。thinking、cache 内訳、
  iterations、通知の `subagent_tokens` は足さない。
* 保存 transcript に provider は無い。モデルと数値を保持し、provider は null、
  診断は `unknown_provider`。モデル名や親のモデルから補わない。

## Codex 0.165.0

主経路は Zed 経由の multi-agent v2。設定上書きは別プロセスの
`exec --json` で、起動前の `CODEX_HOME` に rollout が作られた経路だけを L2 とする。
根拠は [`testdata/codex/native/capture.json`](../testdata/codex/native/capture.json)。

* `--agent` は返却 task path。`session_meta.agent_path` と
  `SubAgentActivity` の `agent_thread_id` で thread を一意に決める。
  同じ path が複数 thread にあるときは `ambiguous_reference` で、新しいファイルは選ばない。
* 子の ID は `session_meta.id`。共有 `session_id` は子 ID にしない。
* `--execution` は子の `task_started.turn_id`。親子は
  `turn_attribution.parent_turn_id` で結ぶ。
* 経過時間は `task_started` から `task_complete` または `turn_aborted` までの
  envelope timestamp の差。`duration_ms` やログ最終行の時刻は使わない。
* usage は `token_usage_record.usage` を `(thread, turn, response)` ごとに 1 回。
  `turn_token_usage`、`thread_token_usage`、`token_count` は照合用で足さない。
  provider は `session_meta.model_provider`、モデルは `turn_context.model`。

## OpenCode V2 2.2.1

既定経路は対話型セッション。L2 は起動前の `OPENCODE_DB=<file>` に
P→T→G が実際に作られた経路。根拠は
[`testdata/opencode-v2/native/capture.json`](../testdata/opencode-v2/native/capture.json)。

* `--agent` は subagent tool の `metadata.sessionID`。再利用時の
  `input.sessionID` も同じ session を指す。
* `--execution` は子 session の user `session_message.id`。
* 終端は、次の user より前の `idle`。`idle.outcome` を状態にする。
  親 tool の `completed` は spawn 完了であり、子の終了ではない。
* 経過時間は `idle.data.time.created - user.data.time.created`。
* usage は assistant `data.tokens` を `native.responseId` ごとに 1 回。
  responseId が無いときだけ message id を使う。一致しない重複は採用しない。
* native total は無いので `total` は null。このことだけでは partial にしない。
* message usage が欠けていても、session 作成時刻がその user の直前であり、
  記録上その session の user が 1 件だけであるときだけ、session の累積値を
  ゼロ baseline の増分として使う。過去の message を消して残った 1 件は新規とみなさない。

読取は存在するファイルを `mode=ro` の read transaction で行う。ファイルが無いときに
DB を作らない。WAL の committed 行は本体ファイルだけをコピーして読まない。
書き込み、migration、checkpoint は行わない。

## 診断

| code | 意味 |
| --- | --- |
| `invalid_source` | 明示パスまたは製品設定が無い、空、または OpenCode にディレクトリを渡した |
| `empty_source` | 保存先に記録が無い |
| `source_not_found` | 既定の保存先が無い |
| `malformed_record` | DB または記録を読めない |
| `agent_not_found` | 返却 ID が保存先に無い。別の成功した実行は選ばない |
| `ambiguous_execution` | 同じ agent に複数の実行がある |
| `ambiguous_reference` | 同じ Codex task path が複数 thread に解決される |
| `missing_descendant` | 子の参照だけがあり、実行記録が無い |
| `missing_execution_id` | agent の usage はあるが、開始 ID が無い |
| `missing_boundary` / `invalid_boundary` | 終端が無い、または終端が開始より前 |
| `unknown_provider` / `unknown_model` | 帰属が記録に無い。数値は残す |
| `duplicate_conflict` | 同じ response/message の usage が一致しない |
| `missing_message_id` | assistant usage に message id が無く、重複を確定できない |
| `unknown_counter` | 同じモデルの subtree 合算で、一部の実行だけ counter が欠けている |
| `unsupported_record` | 計測に必要な未知の record type |
| `truncated_record` | JSONL の末尾が欠けている |

`completeness` が `incomplete` のとき終了コードは 1、`complete` または `partial`
のとき 0 である。実行の `interrupted` や `running` はデータの完全性とは別である。
