# OpenCode V2 実ログ由来 fixture

[VIS-476](https://linear.app/visros/issue/VIS-476) の **OpenCode V2 分**。
2026-11-10 に、本物の OpenCode 2.2.1 が専用の小さな読み取りタスクを
subagent で実行した記録である。共通 envelope には変換していない。
アダプターと CLI の実装・接続検証は [VIS-474](https://linear.app/visros/issue/VIS-474)、
集計契約は [VIS-473](https://linear.app/visros/issue/VIS-473) が担当する。
純 Codex CLI や Claude Code の実行を模倣したデータは含めない。

## ファイルと検証

| ファイル | 用途 |
| --- | --- |
| `records/r1.db` | 再利用前の親 P と正常終了した子 T.A。R1 / L1 の最小入力 |
| `records/default.db` | P、T の A/B、並行した兄弟 R2.T / R2.S |
| `records/running.db` | background 子の shell tool が `running` の snapshot |
| `records/interrupted.db` | 中断前後の子と native `idle.outcome=interrupted` |
| `records/override.db` | 起動前に `OPENCODE_DB` を指定して作った P→T→G の独立 DB |
| `records/api/*.json` | 公式 active/interrupt API の最小化した応答 |
| `records/inventory.json` | 匿名化後 SHA-256 と採取時に選択した行 |
| `capture.json` | 版、起動条件、実採取・未採取範囲、プライバシー変換 |
| `expected.json` | native から独立に固定した境界、参照、counter、時刻、行 allowlist |
| `variants.json` / `prepare.py` | SQLite コピーへ欠損等を注入する derived ケースと探索配置 |
| `verify.py` | hash、DDL、行、匿名化、参照、算術、加工ケースを独立検証 |

```sh
python3 testdata/opencode-v2/native/verify.py
go test ./...
go build ./...

# 新規の出力先を指定する。既存ディレクトリは上書きしない。
python3 testdata/opencode-v2/native/prepare.py ./opencode-v2-derived-fixtures
```

`records/*.db` は SQLite ファイルである。JSONL の末尾切断レシピは適用しない。
`prepare.py` は匿名化済み DB のコピーだけを変更し、比較用 `hashes.json` を出力する。
検証成功は **fixture の検証**であり、現在の CLI が OpenCode V2 native DB を
読めるという意味ではない。期待値は agent-metrics の reader、集計コード、CLI 出力から
生成していない。

## 固定した採取条件

OpenCode **2.2.1**、macOS **27.0.1** build **26A434**、arm64。
実行ファイル SHA-256 は `capture.json` に固定した。provider/model/variant は
`openai` / `gpt-5.6-sol-fast` / `max`。既定経路は対話型 OpenCode セッションで、
親 P から native `subagent` tool を同期・background の両方で呼び出した。

native DB は `<home>/.local/share/opencode/opencode.db` の `project`、`session_v2`、
`session_message`。稼働中 DB を直接配布せず、SQLite Online Backup で私有 snapshot を
作った。採取時は snapshot を `mode=ro&immutable=1` で開き、許可した schema と行だけを
**新しい DB** へ書いた。live DB、WAL、SHM、削除済み page は fixture にコピーしていない。

L2 は別プロセスの起動前に `OPENCODE_DB=<isolated-database-file>` を設定した。
そのファイルへ実際に P→T→G が作られたことを確認しており、既定 DB のコピーを
移動したことを証拠にしていない。
採取時の agent は P=`build`、T=採取用 custom agent `capture-delegator`、G=`explore`。
親子の関係は保存された subagent 呼び出しと session の参照から検証できる。

## 採取台帳

P=親、T=対象、S=兄弟、G=孫。A/B は説明用の呼び名で、native DB へ共通 task ID を
追加していない。実 ID は一貫した仮名に置換している。

| 条件 | 状態 | 操作・証拠・限界 |
| --- | --- | --- |
| R1 / L1 | 実採取済み | P が T を同期生成。T.A が小さな read を行い `idle=succeeded` になった時点を保存 |
| R2 兄弟 | 実採取済み | P が R2.T / R2.S を background で並行生成。対象 T の subtree に兄弟 S を含めない |
| R2 孫 | 実採取済み（L2 経路） | 独立 DB の T が G を同期生成。T→G の session 親参照と subagent 返却参照を保存 |
| R3 再利用 | 実採取済み | T.A 完了後、同じ sessionID を渡して T.B を依頼。A の非ゼロ累積値と B 後の累積値を保存 |
| R4 1依頼の複数 turn | 未採取 | 今回の A/B は別依頼。1依頼内の複数 turn を示す native 根拠は採っていない |
| R5 実行中 | 実採取済み | active API が `running`、子の末尾 assistant に `shell` tool `status=running`、idle なし |
| R5 中断 | 実採取済み | active→interrupt 応答 `interrupted=true`→active から消失。DB は `idle.outcome=interrupted` |
| R5 明示 failed | 未採取 | tool 単体エラーを agent の failed と見なしていない |
| R6 基本 usage | 実採取済み | input/output/reasoning/cache read/cache write を保持。cache write の 0 は数値 |
| R6 追加差分 | 未採取 | モデル切替、counter reset、fork、compaction は発生させていない |
| R7 非永続 | 未採取 | 子記録を回収できる非永続経路は試していない。不適用とは判断しない |
| L2 保存先上書き | 実採取済み | 起動前 `OPENCODE_DB` と、その DB だけに作られた P→T→G |
| L3 明示指定と競合 | 加工準備済み | explicit=r1、configured=override、default=再利用 DB。上位指定を優先 |
| L4 欠損・取り違え | 加工準備済み | invalid、missing、empty、no-target を別々に生成。無効な上位指定から暗黙 fallback しない |

未確認項目を「不適用」にしていない。このデータだけで VIS-476 全体の完了や、
全 OpenCode 起動方式・将来版への対応を主張しない。

## native 参照、実行境界、counter

親 assistant の subagent tool は `state.metadata.sessionID` で子を返す。
再利用時は次の呼び出しの `state.input.sessionID` に同じ値が入る。子 `session_v2.parent_id`
は委譲元 session を指す。background tool の `completed` は **spawn 完了**であり、
子の終了ではない。同期 tool は child user より前に `ran` となり、child idle 後に
`completed` となる。

1 execution は、子 `session_message` の user から、次の user より前にある最初かつ唯一の
idle まで。再利用 session は複数 execution を含む。idle のない open tail は running。
elapsed は `idle.data.time.created - user.data.time.created` で、親 tool 時刻や最終 usage
時刻を代用しない。SQL `time_created` と JSON `data.time.created` は一致させる。

assistant `data.tokens` は message ごとの増分で、選択 execution 内の native
`responseId` ごとに1回加算する。`responseId` がなければ `session_message.id` を
fallback にし、同じ response の重複行は usage/model が一致する場合だけ1回と数える。
`session_v2.tokens_*` は session 生涯の累積値。T.B は
`final cumulative - T.A baseline` と per-message 和が一致する。OpenCode 2.2.1 の今回の
記録には **native total counter がない**ため、`total_tokens` は加算合成せず `null`。

| 実行 | input | output | reasoning | cache read | cache write | elapsed ms | 状態 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| T.A@r1 | 10,200 | 66 | 211 | 24,704 | 0 | 10,099 | succeeded |
| T.B | 303 | 49 | 87 | 24,448 | 0 | 7,429 | succeeded |
| R2.T | 1,736 | 66 | 226 | 33,152 | 0 | 9,714 | succeeded |
| R2.S | 1,700 | 61 | 232 | 33,152 | 0 | 12,033 | succeeded |
| R5.running | 199 | 28 | 115 | 10,496 | 0 | null | running |
| R5.interrupted | 214 | 65 | 298 | 10,496 | 0 | 37,233 | interrupted |
| L2.T | 1,544 | 131 | 204 | 2,560 | 0 | 17,175 | succeeded |
| L2.G | 11,691 | 57 | 107 | 21,376 | 0 | 7,284 | succeeded |

L2.T subtree は T self + G で input 13,235、output 188、reasoning 311、
cache read 23,936、cache write 0。孫の elapsed は対象 T の elapsed に加算しない。
running は既知小計を保持するが、終了時刻・elapsed・最終値は不明。interrupted は
実行状態であり、観測記録が揃っていることとは別に扱う。

## derived ケースと探索優先順位

`variants.json` は、参照された孫の欠損、user/idle 境界の欠損、model 欠損、
message usage 欠損（新規 session の累積値は残す）、responseId 重複、再利用履歴 A の除去、
逆転時刻、未知 message type、合成 body sentinel を記述する。`prepare.py` は各操作を
SQLite transaction と `VACUUM` でコピーへ適用し、元 DB を変更しない。

将来の VIS-474 実装で探索する場合の期待順序は、明示 `--records`、起動前に設定された
`OPENCODE_DB`、既定 DB。上位指定が invalid / missing のとき下位へ暗黙 fallback しない。
valid-empty は「記録なし」、no-target は「要求 session なし」であり、0 usage の正常完了や
別 session の成功として扱わない。

## 採取方針と実施済みの匿名化

匿名化済み fixture と独立期待値を検証入力として保守する。採取時だけ使った
版・日付・私有入力に依存する変換スクリプトは同梱しない。追加採取では、対象版に
必要な匿名化を私有領域で行い、元記録との数値・参照・時刻差の一致を確認する。

1. 実際の OpenCode 版、実行ファイル hash、OS、model/variant を先に固定する。
2. native subagent で小さな read-only task を実行し、返却 sessionID、親子参照、user/idle、
   message token、session cumulative を記録する。実行中と中断は公式 API 応答も保存する。
3. 稼働 DB から SQLite Online Backup を私有領域へ作り、live DB/WAL/SHM はコピーしない。
4. 私有 snapshot から許可した schema と行だけを新規 DB へ再構成し、本文・秘密・端末情報を
   除く。今回の全 epoch ms は **−313 日**移動し、時刻差、数値、型、null/0、順序を保持した。
5. 私有 native query から期待値を独立計算し、`verify.py` で匿名化後 fixture と照合する。

prompt/message/reasoning/tool 本文、command、path、認証・account、private ID 対応表、
private source filename は配布しない。本文欄は空文字にし、参照に必要な
sessionID/call ID/responseId は一貫した
仮名へ置換した。project worktree/directory は `fixture-workspace`。全 DB は freelist 0、
WAL/SHM なし、固定 DDL・行 allowlist・foreign key・hash を検証する。

`records` は `real-derived`。`variants.json` の生成物は `derived`。
body-sentinel だけは漏えい検知用の無害な合成文字列であり、実ログ本文ではない。

## レビュー

contract-first-test-design **Tier 2**。外部 native schema、永続 DB、プライバシー境界を
独立レビューした。`verify.py` は配布 fixture と独立期待値を直接照合し、親子関係、
R3 baseline、running/interrupted、行 allowlist、clock/counter/boundary を検証する。
DDL は table SQL の hash と index 定義で照合する。加工ケースは生成物と元記録の不変性を
検証する。未解決の責任境界・trust boundary の争点はなく **Tier 3 不要**。

上記は VIS-474 の実装入力である。現行 agent-metrics CLI の OpenCode V2 native 対応成功は
この変更では主張しない。
