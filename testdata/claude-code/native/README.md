# Claude Code 実ログ由来 fixture

[VIS-476](https://linear.app/visros/issue/VIS-476) の **Claude Code 分**。
2026-11-10 に、本物の Claude Code 2.1.312 が専用の小さな読み取りタスクを
サブエージェントで実行した記録である。共通 envelope に変換していない。
アダプターと CLI の実装・接続検証は [VIS-474](https://linear.app/visros/issue/VIS-474)、
集計契約は [VIS-473](https://linear.app/visros/issue/VIS-473) が担当する。
Codex / OpenCode の実行を模倣したデータは含めない。

## ファイルと検証

| ファイル                                | 用途                                                                   |
| --------------------------------------- | ---------------------------------------------------------------------- |
| `records/r1/projects/…`                 | 再利用前の親 P と正常終了した子 T.A。R1 / L1 の最小入力                |
| `records/default/projects/…`            | P、T の A/B/C、孫 G.B / G.C、兄弟 S の A/B/C（C は中断）、S の孫 GS    |
| `records/running-grandchild/projects/…` | S.B が handback 済みで停止前、GS が実行中の snapshot                   |
| `records/running/projects/…`            | S.C の実行中 snapshot                                                  |
| `records/override/projects/…`           | `CLAUDE_CONFIG_DIR` を起動前に変更した `claude -p` の親 O と子 O.child |
| `records/ephemeral/projects/…`          | `--no-session-persistence` 実行後に残った唯一のファイル（meta.json）   |
| `records/events/*.jsonl`                | `claude -p --output-format stream-json` の実 stdout。L2 と R7          |
| `records/inventory.json`                | 匿名化後の SHA-256 と、各行に対応する私有元 snapshot の行番号          |
| `capture.json`                          | バージョン、起動条件、実採取の状態、保存先検証の記録                   |
| `expected.json`                         | 固定した独立期待値、参照、ケース、各値の native ファイル/行/message ID |
| `variants.json` / `prepare.py`          | コピーに欠損等を注入する `derived` ケースと L3/L4 の配置レシピ         |
| `verify.py`                             | fixture の hash、匿名化、ID、境界、独立算術、加工ケースの検証          |

```sh
python3 testdata/claude-code/native/verify.py
go test ./...
go build ./...

# 新規の出力先を指定する。既存ディレクトリは上書きしない。
python3 testdata/claude-code/native/prepare.py ./claude-code-derived-fixtures
```

`records/` 以下は native のバイト列（1 行 JSON の meta.json を含む）を保つ。
JSON フォーマッタ（lefthook の biome など）で整形すると inventory の hash が変わるので整形しない。

検証成功は **fixture の検証** であり、CLI の native 対応成功ではない。
期待値は agent-metrics の reader / 集計コードや CLI 出力から生成していない。
私有元 snapshot を別スクリプトで集計して固定し、`verify.py` が匿名化後の
fixture から再計算して一致を確認する。

## 固定した採取条件

Claude Code **2.1.312**（native `version` フィールドも同値）。macOS **27.0.1**、
build **26A434**、arm64。実行ファイルの SHA-256 は `capture.json` にある。

主経路は対話型 CLI セッション（`entrypoint=cli`、permission mode `auto`）。
親 P は `claude-opus-5-5`（effort high）。子は Agent ツールの `general-purpose`
で、background 実行（meta `requestShape=background`）。孫には `model: "haiku"`
を指定し `claude-haiku-5-5` に解決された。当該プロセスの `CLAUDE_CONFIG_DIR` は
未設定で、既定ルート `~/.claude/projects` の下に生成された JSONL を読み取った。

上書きの検証は同じ版の **別 `claude -p` プロセス**。起動前に `CLAUDE_CONFIG_DIR`
を専用ディレクトリへ設定し、実際にそのディレクトリの `projects/` に親子の
transcript が生成された。この経路の子は foreground（`requestShape=foreground`）。
コピーの配置変更を L2 の証拠にしていない。

## 採取台帳

P=親、T=対象、S=兄弟、G=孫。A/B/C は説明用の呼び名で、native 記録へ共通
task ID は追加していない。実 ID は一貫した仮名に置換している。

| 条件                      | 状態                   | 操作・証拠・限界                                                                                                                                                                     |
| ------------------------- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| R1 / L1                   | 実採取済み             | P が Agent で T を生成。T.A が README の先頭行を読み終了。再利用前に r1 を採取                                                                                                       |
| R2 兄弟・孫               | 実採取済み             | T.B と S.A を並行実行。T.B が G.B（haiku）を生成し結果を待つ。孫は `parentAgentId` と `spawnDepth=2` を持つ                                                                          |
| R3 再利用                 | 実採取済み             | 同じ T に SendMessage で B、次に C。実行無指定では A/B/C の選択が必要                                                                                                                |
| R3 累積 baseline          | 観測記録に存在しない   | transcript の usage は message 単位のみ。累積カウンタはない。過去の依頼 A の記録は保持                                                                                               |
| R4 1依頼の複数 turn       | 実採取済み             | T.B は G.B を待つ間に `end_turn` で停止し、G.B の handback（`origin.kind=peer`）と task-notification で再開した。coordinator 記録を挟まない同じ区間で、親への停止通知は最後の1回だけ |
| R5 実行中・中断           | 実採取済み             | S.C に順次読み取りを依頼し、毎秒 snapshot の途中を `running`、`TaskStop` の後を default とした                                                                                       |
| R5 親 handback / 孫実行中 | 実採取済み             | S.B が GS を起動してすぐ handback。その時点の snapshot に GS の終端も S.B の停止通知もない                                                                                           |
| R5 親完了 / 孫実行中      | 通常完了では到達しない | 停止通知は子の background 子孫が残る間は発火しない（通知本文の native 注記と、T.C / S.B の観測）。親が handback 後も孫の報告を受けて turn を消費するため、handback を終端にしない    |
| R5 親中断 / 孫実行中      | 未採取                 | 孫が生きている親への TaskStop は試していない                                                                                                                                         |
| R5 明示 failed            | 未採取                 | G.C の Bash がハーネスに拒否された `is_error` はツール単体の失敗で、G.C 自体は完了した                                                                                               |
| R6 基本 usage             | 実採取済み             | cache creation / read、thinking の非ゼロ、1h cache creation の観測 0 を保持                                                                                                          |
| R6 モデル混在             | 実採取済み             | T.B / T.C / S.B の subtree は opus と haiku の混在                                                                                                                                   |
| R6 その他                 | 未採取                 | 同一実行内のモデル切替、fork、compaction は未観測。大量トークンで発生させていない                                                                                                    |
| R7 非永続                 | 実採取済み（部分）     | `--no-session-persistence` で transcript は0件、meta.json だけ残った。stdout に子の ID・状態・時間はあるが、子の usage は確定しない                                                  |
| L2 保存設定上書き         | CLI 経路で実採取済み   | 別プロセス起動前の `CLAUDE_CONFIG_DIR` と、実際にそこへ生成された親子 transcript                                                                                                     |
| L3 明示指定と競合         | 加工準備済み           | `prepare.py` の `lookup/explicit`=r1、configured=override、default=再利用履歴                                                                                                        |
| L4 欠損・取り違え         | 加工準備済み           | nonexistent、empty、no-target（無関係な O.child）                                                                                                                                    |

未確認項目を「不適用」にしていない。このデータだけで VIS-476 全体の完了や
全 Claude Code 起動方式（IDE / Desktop / SDK など）への対応を主張しない。

## native 参照と期待値

返却参照は Agent の `tool_result.toolUseResult.agentId`（`status=async_launched`、
`resolvedModel`）。foreground では同じ位置に `status=completed` と
`totalDurationMs` / `totalTokens` / `usage` が入る。子の transcript は
`<session>/subagents/agent-<agentId>.jsonl`。その `meta.json` の `toolUseId` が
親の Agent `tool_use.id` と一致する。孫は同じディレクトリに平置きで、
`parentAgentId` が委譲元を示し、委譲元 transcript の中に Agent 呼び出しがある。

再利用は親の `SendMessage`（`input.to` が agentId、結果の `resumedAgentId`）。
子 transcript では `origin.kind=coordinator` の user 記録が次の依頼の開始になる。
**SendMessage 呼び出しと子の coordinator 記録を直接結ぶ ID は見つかっていない。**
同じ agent への参照の順序で対応づけている。`promptId` は親セッションの
プロンプト ID で全依頼に共通なので、依頼の識別に使ってはいけない。
依頼の選択は、子の開始記録の `uuid`（`expected.json` の `start_line`）で行う。

**実行区間**は、開始記録から次の coordinator 記録の直前まで。
**終端**は、区間内で最後のループ終端記録である。終端記録は、`stop_reason=end_turn`
の assistant（message 単位で1回）、`toolEndsTurn=true` の tool_result、
`[Request interrupted by user]` のいずれか。さらに、その後に呼び出し側が
停止通知を受けている必要がある。停止通知は、親の `queue-operation enqueue`
（孫の場合は委譲元の task-notification user 記録）で `status=completed|killed`
のもの、または foreground の tool_result `status=completed`。
停止通知のない終端記録は、中間 turn の終わりと区別できない。

usage は assistant の `message.id` ごとに1回だけ数える。1 message が content
block ごとに複数行へ分割され、同じ `usage` / `requestId` が繰り返されるためである。
`total = input + cache_creation + cache_read + output`。
`output_tokens_details.thinking_tokens` は output の内数、
`cache_creation.ephemeral_*` は cache_creation の内数、`iterations[]` は
同じリクエストの再掲であり、どれも加算しない。
通知の `subagent_tokens` と foreground 結果の `totalTokens` は**最後の message の
コンテキスト量**で、消費量の合計ではない。

| 実行                      | model | input | cache 作成 | cache 読取 | output | thinking |              total | 境界間 ms | 通知 ms |
| ------------------------- | ----- | ----: | ---------: | ---------: | -----: | -------: | -----------------: | --------: | ------: |
| T.A                       | opus  |     4 |     13,718 |     11,533 |    159 |        0 |             25,414 |     5,437 |   5,471 |
| T.B self                  | opus  |    14 |      2,053 |     57,585 |    469 |      159 |             60,121 |    14,826 |  14,855 |
| G.B                       | haiku |     4 |     13,780 |     11,515 |    239 |       73 |             25,538 |     4,240 |   4,259 |
| T.C self                  | opus  |    14 |      2,489 |     66,070 |    633 |      172 |             69,206 |    24,817 |  24,817 |
| G.C                       | haiku |     6 |      8,107 |     31,967 |  1,246 |      777 |             41,326 |    12,547 |  12,557 |
| S.A                       | opus  |     4 |      7,102 |     18,152 |    158 |        0 |             25,416 |     3,514 |   3,528 |
| S.B self                  | opus  |    14 |      2,350 |     58,105 |    667 |      187 |             61,136 |    23,452 |  23,465 |
| GS                        | haiku |    18 |      8,167 |    118,292 |  1,058 |      157 |            127,535 |    12,431 |  12,438 |
| S.C 中断                  | opus  |     8 |        778 |     65,905 |    419 |        0 | 67,110（既知小計） |     9,713 |    なし |
| S.C 実行中                | opus  |     4 |        541 |     32,563 |    212 |        0 |     33,320（小計） |      null |    なし |
| S.B 実行中（handback 済） | opus  |     6 |      1,181 |     27,825 |    448 |      102 |     29,460（小計） |      null |    なし |
| GS 実行中                 | haiku |    12 |      7,800 |     74,676 |    789 |      157 |     83,277（小計） |      null |    なし |
| 上書き経路 O.child        | opus  |     4 |     11,581 |     10,914 |     98 |        0 |             22,597 |     4,346 |   4,414 |

T.A は `(2+11,533+0+102) + (2+2,185+11,533+57) = 11,637 + 13,777 = 25,414`。
通知の `subagent_tokens=13,777` は2つ目の message 単体の値である。
T.B subtree は opus 60,121 + haiku 25,538（合計 85,659）、T.C subtree は
opus 69,206 + haiku 41,326、S.B subtree は opus 61,136 + haiku 127,535。
モデル別に保持し、孫の時間を対象の時間へ加算しない。

T.B を最初の `end_turn` で打ち切ると 5,704 ms になり誤りである（R4）。
S.B の handback は開始から 9,040 ms、停止は 23,452 ms。handback 後も孫の報告
処理で usage が増えるので、handback を終端に使わない。
S.C は `TaskStop` の時点で、応答のない `api-request` が1件送信中だった
（`unanswered_request_lines`）。その usage は記録されていないため、S.C の usage は
既知小計で、完全性は partial とする。停止通知 `status=killed` には usage がない。

期待時間は**明示記録の envelope timestamp 同士の差**。通知の `duration_ms` は
別の観測値として保存し、丸めたり同値に書き換えていない。

## R7（非永続）で得られるもの

`--no-session-persistence` の stdout には、子の `agent_id`、spawn の
`tool_use_id`、`task_started`（`is_backgrounded=false`、`spawn_depth`）、
`task_notification`（`status`、`duration_ms`）がある。一方で、stream の
assistant usage は message 開始時点の値で、output_tokens が確定値ではない。
L2 の同じ message では stream 16 に対し transcript 193 だった。また、子の最後の
message は stream に出ない。`result.modelUsage` は親子を合わせたモデル別合計である。
したがって、子の self usage は不明として扱い、0 や complete にしない。
stdout に子が現れないことだけを理由に、子がいないと推論しない。

## 採取方針と実施済みの匿名化

匿名化済み fixture と独立期待値を検証入力として保守する。採取時だけ使った
版・日付・私有入力に依存する変換スクリプトは同梱しない。追加採取では、対象版に
必要な匿名化を私有領域で行い、元記録との数値・参照・時刻差の一致を確認する。

1. [採取 SKILL](../../../.claude/skills/capture-claude-code-metrics/SKILL.md)
   を読み、実際の版・保存先・起動方式を固定する。
2. Agent / SendMessage / TaskStop を実際に使う。元記録を変更せず、完全な行だけを
   リポジトリ外の私有領域へコピーする。実行中の状態はバックグラウンドで毎秒
   snapshot を取り、そこから選ぶ。ハーネスの `sleep` 禁止を回避しない。
3. 私有 snapshot から必要な native レコードを選び、本文・秘密・端末情報を除く。
   今回の時刻移動量は **−313 日**。追加採取では対象版の schema を確認し、全関連記録に
   同じ時刻移動を適用して、共有前に内容を確認する。
4. 元記録から message 単位の和と境界を独立に計算して期待値を固定し、
   `verify.py` で匿名化後と照合する。

除去対象は、prompt / message / thinking / tool の本文、tool 入力（Agent の
description / subagent*type / model、SendMessage の to、TaskStop の task_id 以外）、
tool 結果の本文、system prompt、API request blob / shape、環境・git・classifier の
文脈、出力ファイルのパス、端末の絶対パス、セッションタイトル。
計測と無関係なレコード種別（mode、permission-mode、file-history-snapshot、
attachment の大半など）は除き、残したレコードの順序と `parentUuid` の連鎖は保持した。
通知文字列は `task-id` / `tool-use-id` / `status` / `usage` タグだけに縮約した。
エージェント間メッセージは `<agent-message from="…">` 参照だけを残した。
cwd をエンコードしたプロジェクトディレクトリ名は、`-fixture-workspace-agent-metrics`
と `-fixture-override-ws` に置換した。UUID の version / variant、agentId の形、
`toolu*`/`msg*`/`req*` の接頭辞と長さを保ったまま、全参照を一貫して仮名化した。
全 timestamp と epoch ms を同じ日数だけ移動し、数値カウンタ・duration・時刻差・型・
null は変えていない。

`records` は `real-derived`。`variants.json` による欠損・重複・切断等は `derived`。
body-sentinel の追加レコードだけは無害な**合成**検出文字列である。

## VIS-474 への接続例（実装後の確認用）

```sh
agent-metrics collect --product claude-code --agent ada3d4768bc8b8ec8 \
  --records testdata/claude-code/native/records/r1/projects

agent-metrics collect --product claude-code --agent ada3d4768bc8b8ec8 \
  --execution b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8 \
  --records testdata/claude-code/native/records/default/projects

CLAUDE_CONFIG_DIR=./testdata/claude-code/native/records/override \
  agent-metrics collect --product claude-code --agent a902a36167c8ba175
```

上記コマンドの成功はこの変更では主張しない。既定探索を fixture で試す場合は、
専用のテスト用 home の `.claude/projects` へコピーし、実利用者の保存先を変更しない。

## 一次資料

[Subagents（再開と別 transcript）](https://code.claude.com/docs/en/sub-agents#resume-subagents)、
[サブエージェントのネスト](https://code.claude.com/docs/en/sub-agents#let-subagents-spawn-their-own-subagents)、
[環境変数 CLAUDE_CONFIG_DIR](https://code.claude.com/docs/en/env-vars)。
これらを全版の transcript schema の保証とは扱わず、今回のフィールドは実記録で確認した。
