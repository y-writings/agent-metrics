# Codex 実ログ由来 fixture

[VIS-476](https://linear.app/visros/issue/VIS-476) の **Codex 分**。
2026-11-08 に、本物の Codex が専用の小さな読み取りタスクを実行した記録である。
共通 envelope に変換していない。アダプターと CLI の実装・接続検証は
[VIS-474](https://linear.app/visros/issue/VIS-474)、集計契約は
[VIS-473](https://linear.app/visros/issue/VIS-473) が担当する。
Claude Code / OpenCode の実行を模倣したデータは含めない。

## ファイルと検証

| ファイル | 用途 |
| --- | --- |
| `records/r1/sessions/…` | 再利用前の親 P と正常終了した子 T。R1 / L1 の最小入力 |
| `records/default/sessions/…` | P、T の A/B/C、兄弟 S、正常終了した孫 G.B、実行中の孫 G.C |
| `records/running/sessions/…` | S の中断前スナップショット。過去の完了した依頼も保持 |
| `records/override/sessions/…` | CODEX_HOME を起動前に変更した CLI の親 O と子 O.child |
| `records/events/*.jsonl` | CLI の実 stdout。通常モードと非永続モードの対照 |
| `records/inventory.json` | 匿名化後の SHA-256 と、各行に対応する私有元スナップショットの行番号 |
| `capture.json` | バージョン、起動条件、実採取の状態、保存先検証の記録 |
| `expected.json` | 固定した独立期待値、対象/除外範囲、各値の native ファイル/行/response ID |
| `variants.json` / `prepare.py` | コピーに欠損等を注入する `derived` ケースと L3/L4 の配置レシピ |
| `verify.py` | fixture の hash、匿名化、ID、境界、独立算術、加工ケースの検証 |

```sh
python3 testdata/codex/native/verify.py
go test ./...
go build ./...

# 新規の出力先を指定する。既存ディレクトリは上書きしない。
python3 testdata/codex/native/prepare.py ./codex-derived-fixtures
```

`prepare.py` は匿名化済みデータだけを複製する。重複する native ファイルを
大量にコミットする代わりに、加工操作と期待する失敗理由をレシピで保存する。
生成物には比較用 `hashes.json` が入る。製品 reader のテストでは読み取り前後の
hash を比較する。現時点の検証成功は **fixture の検証** であり、CLI の native
対応成功ではない。期待値は reader/集計コードや CLI の出力から生成していない。

## 固定した採取条件

Codex CLI/native `cli_version` は **0.165.0**。OS は macOS **27.0.1**、
OS build **26A434**、arm64。バイナリ識別 hash は `capture.json` にある。
モデル/provider は全実採取で `gpt-6-astra` / `openai`。

主経路は稼働中の Zed 経由 Codex（native `source=vscode`, `originator=zed`,
multi-agent v2）。親は foreground、子は collaboration ツールで動作した。
親プロセスから子を起動し、返却値と同一プロセス系列の保存ログを確認した。
当該プロセスの `CODEX_HOME` は未設定で、既定ルートの `sessions/YYYY/MM/DD/`
に生成された JSONL を読み取った。SQLite や Entire を採取根拠にしていない。

上書きの検証は同じ版の **別 CLI プロセス**。起動前に CODEX_HOME を専用ルートへ
設定し、`exec --json --ignore-user-config --enable multi_agent --enable multi_agent_v2`
で読み取り専用タスクを実行した。そこで実際に親子の rollout が生成された。
コピーの配置変更を L2 の証拠にしていない。既に稼働中の Zed プロセスが、後から
変更した端末環境変数に追従するとは主張しない。

## 採取台帳

P=親、T=対象、S=兄弟、G=孫。A/B/C は説明用の呼び名であり、native 記録へ
共通 task ID を追加していない。各実 ID は一貫した仮名に置換している。

| 条件 | 状態 | 操作・証拠・限界 |
| --- | --- | --- |
| R1 / L1 | 実採取済み | P が `capture_target` を `fork_turns=none` で生成。T.A が README の先頭行を読み正常終了。終了後、再利用前に r1 を採取 |
| R2 兄弟・孫 | 実採取済み | P が T.B と S.A を並行実行。T.B が G.B を生成し終了を待つ。子と孫は独自の thread/turn を持つ |
| R3 再利用 | 実採取済み | 同じ T に A 完了後 B、B 完了後 C を依頼。B 開始前の非ゼロ累積値を保存。実行無指定では A/B/C の選択が必要 |
| R4 1依頼の複数 turn | 未採取・要調査 | A/B/C は完了後の別依頼。複数ツール呼び出しや待機を複数 turn の証拠にしない。中断後の継続を同一依頼へ結ぶ native 根拠は未確認 |
| R5 実行中・中断 | 実採取済み | S を新しい依頼で再利用。SKILL/README の読み取り後、短い sleep 中に snapshot、通常の `interrupt_agent`、中断後 snapshot |
| R5 T完了/G未完了 | 実採取済み | T.C に G.C の起動後すぐ返答させた。T の terminal 後も G の usage が増え、保存した snapshot に G の terminal はない |
| R5 明示 failed | 未採取 | ツール単体の失敗を agent の failed に数えていない |
| R6 基本 usage | 実採取済み | request 増分・turn 累積・thread 累積・token_count 複写を照合。cache read / reasoning の非ゼロと cache write の観測 0 を保持 |
| R6 追加差分 | 未採取 | 別モデル混在、同一実行のモデル切替、成功した履歴 fork、compaction、counter reset は未観測。大量トークンで無理に発生させていない |
| R7 非永続 | 試行済み、子データ未採取 | `exec --ephemeral --json` を fork all / none で各1回試行。親の stdout は採取したが子への参照・usage は回収できず、保存 rollout は増えなかった。子が存在しない/機能が不適用という証明ではない |
| L2 保存設定上書き | CLI 経路で実採取済み | 別プロセス起動前の CODEX_HOME と、実際にそのルートへ生成された親子ログ。`override` を参照 |
| L3 明示指定と競合 | 加工準備済み | `prepare.py` の `lookup/explicit`=r1、configured=override、default=再利用履歴。無効な上位指定でも下位へ暗黙 fallback しない |
| L4 欠損・取り違え | 加工準備済み | nonexistent、empty、no-target（後発の無関係な O.child）を生成。明示指定無効と CODEX_HOME 無効を別ケースにする |

未確認項目を「不適用」にしていない。このデータだけで VIS-476 全体の完了や
全 Codex 起動方式への対応を主張しない。

## native 参照と期待値

返却された参照は `/root/capture_target` 等の **task path**。
`function_call.call_id` と `event_msg/item_completed.item.id` が一致する
`SubAgentActivity` の `agent_path` / `agent_thread_id` が thread UUID への橋渡しになる。
`function_call_output.output` は JSON **文字列**のまま返却参照を保持する。
`arguments` も元の文字列型を保ち、task_name/target 等の参照だけを残す。

子の `session_meta.id` がその thread、`parent_thread_id` が親 thread。
**子の `session_id` は共有ルートセッションを指す**ため、子 ID に使ってはいけない。
子の `task_started.turn_attribution.parent_turn_id` が親実行を結ぶ。
`turn_trigger` は Zed 経路では null、上書き CLI 経路の親子では `"exec"`。
call ID から子 turn へ直接対応するフィールドがあるとは主張しない。
同じ T の複数依頼は、子の各 `task_started.turn_id` で選択する。
親子の turn ID 一致や、同じ場所・同時刻という推測で関連付けない。

usage は `(thread_id, turn_id, response_id)` ごとの `token_usage_record.usage` の和。
`turn_token_usage`、`thread_token_usage`、`event_msg/token_count.info` は同じ消費の
別表現であり加算しない。モデルは当該 turn の `turn_context.model`、provider は
`session_meta.model_provider`。`cached_input_tokens` は input の内数、
`reasoning_output_tokens` は output の内数。今回 total=input+output で照合できる。
cache write は全例で **観測 0**。未提供と取り違えない。

| 実行 | input | cache read | output | reasoning | total | 明示イベント間 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| T.A | 40,004 | 26,752 | 76 | 0 | 40,080 | 8,994 |
| T.B self | 82,038 | 81,024 | 381 | 123 | 82,419 | 43,699 |
| G.B | 40,162 | 39,680 | 201 | 86 | 40,363 | 30,530 |
| T.B subtree | 122,200 | 120,704 | 582 | 209 | 122,782 | 対象 T の 43,699 |
| S 実行中 | 52,149 | 45,824 | 558 | 370 | 52,707（小計） | null |
| S 中断後 | 78,961 | 72,448 | 614 | 370 | 79,575 | 31,255 |
| T.C self | 107,590 | 100,864 | 592 | 251 | 108,182 | 31,270 |
| G.C 実行中 | 60,669 | 53,248 | 258 | 83 | 60,927（小計） | null |
| 上書き経路 O.child | 36,134 | 29,824 | 40 | 0 | 36,174 | 8,130 |

T.B の request total は `20,496 + 20,562 + 20,611 + 20,750 = 82,419`。
thread 累積でも `122,499 − 40,080 = 82,419`。T.A の過去分を混ぜない。
T.C subtree の既知小計は `108,182 + 60,927 = 169,109` だが最終総量は不明。
T.C の時間と self usage は確定したまま保持する。

期待時間は **明示 lifecycle レコードの envelope timestamp 同士の差**。
`task_started` から `task_complete` / `turn_aborted` までを採る。
これはログ最終行の時刻でも、内部のより細かい実行 duration でもない。
例として T.A は envelope 差 8,994 ms、native `duration_ms` 9,046 ms、
整数 `completed_at−started_at` は 9,000 ms。T.B はそれぞれ
43,699 / 43,708 / 44,000 ms。全部を保存し、丸めたり同値に書き換えていない。
VIS-474 で別の時間定義を選ぶなら、別観測値として明示的に扱う。

`expected.json` の `complete` は対象実行の記録が揃うという意味。
中断は `status=interrupted` とデータ完全性を分離する。実行中は既知小計を保持し、
最終値・終了時刻・経過時間は null。R7 の stdout にある usage は **親の usage**
であり、未知の子へ帰属させない。stdout の子 ID 不在だけで子の不在も推論できない。

## 採取方針と実施済みの匿名化

匿名化済み fixture と独立期待値を検証入力として保守する。採取時だけ使った
版・日付・私有入力に依存する変換スクリプトは同梱しない。追加採取では、対象版に
必要な匿名化を私有領域で行い、元記録との数値・参照・時刻差の一致を確認する。

1. [採取 SKILL](../../../.agents/skills/capture-codex-metrics/SKILL.md) を読み、
   実際の版・保存先・起動方式を固定する。`spawn_agent` で小さな read-only task を
   動かす。再利用は `followup_task`、中断は通常の `interrupt_agent` を用いる。
2. 返却された task path を native `SubAgentActivity` と `session_meta` で解決する。
   元記録を変更せず、該当する親子の JSONL をリポジトリ外の私有領域へコピーする。
   実行中 snapshot と完了後 snapshot を区別し、完全な行まで採れたことを確認する。
3. 台帳の手順で R1 → R2/R3 → R5 → 別プロセス L2 を採る。R4/R6/R7 の追加経路を
   採る場合は実施前に live イベントの収録も準備する。
4. 私有 snapshot から必要な native レコードを選び、本文・秘密・端末情報を除く。
   今回専用の時刻移動量は **−311 日**。追加採取では対象版の schema を確認し、
   全関連記録に同じ時刻移動を適用して、共有前に内容を確認する。
5. 元記録の request 和、境界、baseline と匿名化後を独立比較し、固定期待値を作る。
   reader の出力を期待値にコピーしない。`verify.py` と独立レビューで照合する。

除去対象は prompt/message/reasoning/tool 本文、暗号化 reasoning、追加ツール定義、
world state、認証/アカウント metadata、端末のパス、git 情報、レート制限情報。
計測と無関係なレコードは除き、残したレコードの native 階層・順序を維持した。
本文欄が必要な箇所は空文字、元が null の箇所は null のまま。
UUID の version/variant と識別子の prefix/長さを維持して全参照を一貫して仮名化。
全 ISO timestamp、epoch 秒・ms、日付ディレクトリ・ファイル名を同じ日数だけ移動した。
数値カウンタ、duration、時刻差、型、欠落と 0、再利用 baseline は変えていない。
元端末の絶対パスや実 ID 対応表を同梱していない。

`records` は `real-derived`。`variants.json` による欠損・重複・切断等は `derived`。
body-sentinel の追加レコードだけは無害な **合成**検出文字列であり実ログ本文ではない。
曖昧な関連、モデル切替、counter reset の全分岐は共通 unit に委ね、未採取の
製品挙動を捏造した native fixture を増やさない。

私有 snapshot は処理前後で同一、元の live ログは読取りのみと確認した。
製品自身による正常な追記を reader の書き換えと誤認しない。

## VIS-474 への接続例（実装後の確認用）

```sh
agent-metrics collect --product codex --agent /root/capture_target \
  --records testdata/codex/native/records/r1/sessions

agent-metrics collect --product codex --agent /root/capture_target \
  --execution b16afe9d-0ec9-7c62-80d5-b12717dbd878 \
  --records testdata/codex/native/records/default/sessions

CODEX_HOME=./testdata/codex/native/records/override \
  agent-metrics collect --product codex --agent /root/override_child
```

実採取時には返却 task path を標準ルートから解決する。fixture で既定探索を試す
場合は、専用のテスト用 home の `.codex/sessions` へコピーし実利用者の保存先を
変更しない。上記コマンドの成功はこの変更では主張しない。

## レビューと一次資料

contract-first-test-design **Tier 2**：外部 native schema とプライバシー境界が対象。
別エージェントが私有元記録から独立に算術・参照・時刻・匿名化を確認した。
14 ファイル / 302 retained records、105 種類の仮名対応、294 timestamp と
83 epoch フィールドの移動を照合。指摘された stdout 内の null→空文字変換を修正し、
再検証した。追加レビューで起動経路ごとの `turn_trigger` の記述を修正し、L3 の
実行無指定・explicit 1実行/default 3実行/configured 別対象の対照も検証に加えた。
未解決の責任境界の争点はなく **Tier 3 不要**。

採取設計の一次資料：
[App Server の thread/turn・終端・usage・ephemeral 説明](https://learn.chatgpt.com/docs/app-server)、
[環境変数](https://learn.chatgpt.com/docs/config-file/environment-variables)、
[CLI JSONL を用いた実行記録の検証](https://developers.openai.com/blog/eval-skills)。
これらを全版の rollout schema の保証とは扱わず、今回のフィールドは実記録で確認した。
