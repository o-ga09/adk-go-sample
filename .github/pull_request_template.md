# 概要

<!-- この変更で何を実現するのかを1〜3行で -->

## 関連Issue

<!-- 例: Closes #11 / Refs #14。関連Issueがなければ「なし」 -->

## 変更内容

<!-- 変更点を箇条書きで。実装上の判断（なぜこの方式にしたか）があれば書く -->

-

## 動作確認

<!-- 実施したものにチェック。実行結果や確認手順は必要に応じて追記 -->

- [ ] `go build ./...`
- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] `ACTION_MODE=dry_run go run ./cmd/batch`（エージェントの挙動に影響する変更の場合）

## ルール準拠チェック

<!-- 該当する変更がある場合のみチェック。詳細は .claude/rules/ を参照 -->

- [ ] **ツールの追加・変更**: ACTION_MODE のゲートをツール層に実装した（`action-mode-safety.md`）
- [ ] **ツール入出力struct**: オプションフィールドに `omitempty`、結果スライスが `null` にならないことを確認し、jsonschema 回帰テストを追加・更新した（`tool-json-schema.md`）
- [ ] **store / ADKモジュール更新**: 時刻カラムの `type:datetime(6);precision:6` 規約とスキーマstructの upstream 差分を確認した（`mysql-sessions.md`）
- [ ] **Dockerfile / CIワークフロー変更**: インフラリポジトリ側の契約（コンテナ名・バイナリパス・yq書き換え）を壊していないことを確認した（`ci-cd-contract.md`）

## 補足

<!-- レビュアーへの注意点、残課題、スクリーンショットなど。なければ削除 -->
