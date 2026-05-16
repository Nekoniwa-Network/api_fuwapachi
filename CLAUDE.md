# CLAUDE.md — Claude へのプロジェクトルール

このファイルは Claude (AI アシスタント) がこのリポジトリで作業する際に参照するルール集です。

---

## プロジェクト概要

- **プロジェクト名**: Fuwapachi API Server (`api_fuwapachi`)
- **言語/フレームワーク**: Go (1.25+), Gorilla Mux, Gorilla WebSocket
- **用途**: Nekoniwa-Network (Fuwapachi) 向けのバックエンド API および WebSocket サーバー
- **データベース**: MariaDB / MySQL

---

## ディレクトリ構成

```
api_fuwapachi/
├── cmd/
│   └── server/
│       └── main.go       # エントリーポイント
├── internal/             # 外部からインポート不可のアプリケーションコード
│   ├── config/           # 設定管理
│   ├── database/         # DB接続・マイグレーション関連
│   ├── handler/          # API・WebSocketのハンドラー（コントローラー）
│   └── model/            # データモデル構造体・DB操作
├── .env                  # 環境変数（Git 管理外）
├── .env.example          # 環境変数のサンプル
├── go.mod                # Goモジュール定義
├── go.sum                # 依存パッケージのチェックサム
└── README.md             # プロジェクト仕様とドキュメント
```
*(注: `models` や `router`、ルートの `config` や `database` ディレクトリが空で存在している場合はレガシーなディレクトリであり、基本は `internal/` 配下を参照してください)*

---

## コーディングルール

### 言語・スタイル

- **コミュニケーション**: すべて **日本語** で回答・コメントを記述する
- **言語機能**: Go の標準的な作法に従う（エラーハンドリングは `if err != nil` を明示的に行う）
- **フォーマット**: 保存時に `gofmt` (または `goimports`) に準拠したフォーマットを適用する
- **パッケージ**: 機能ごとに適切なパッケージ分割を行い、循環参照（Circular Dependency）を避ける

### 命名規則

| 種別 | 命名規則 | 例 |
|------|----------|----|
| パッケージ | 小文字の単語 | `handler`, `database` |
| ファイル | スネークケース | `message_handler.go` |
| 構造体/インターフェース | キャメルケース (公開は先頭大文字) | `MessageHandler`, `Message` |
| 変数/関数 | キャメルケース (公開は先頭大文字) | `GetMessages()`, `dbConn` |

### アーキテクチャ・実装方針

- **RESTful API**: Gorilla Mux を使用し、標準的なHTTPメソッドとステータスコードを返す
- **WebSocket**: Gorilla WebSocket を使用。並行処理の安全性（`sync.RWMutex`など）を確保する
- **データベース**: `go-sql-driver/mysql` を使用。SQLインジェクション対策としてプレースホルダーを必ず使用する
- **論理削除**: データの削除は `deleted_at` へのタイムスタンプ記録によるソフトデリートを基本とする

---

## Git ブランチ戦略

| ブランチ | 用途 |
|----------|------|
| `main` | 本番リリース用（直接プッシュ禁止） |
| `dev` | 開発統合ブランチ |
| `feature/*` | 機能追加・改修（例: `feature/add-auth`） |
| `fix/*` | バグ修正（例: `fix/websocket-leak`） |
| `hotfix/*` | 緊急修正 |

### コミットメッセージ規則

```
<type>: <日本語の簡潔な説明>

例:
feat: メッセージ取得APIにページネーションを追加
fix: WebSocket切断時のパニックを修正
chore: 依存パッケージを更新
docs: APIドキュメントを更新
```

**type 一覧**: `feat` / `fix` / `docs` / `style` / `refactor` / `test` / `chore`

---

## 環境変数

`.env` ファイルは Git 管理外。新しい環境変数を追加する際は必ず `.env.example` および `README.md` にも反映する。

主要な変数:
- `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`
- `SERVER_PORT`
- `ALLOWED_ORIGINS`

---

## 開発コマンド

```bash
# 依存パッケージのダウンロード
go mod download

# 開発サーバー起動（デフォルト: http://localhost:8080）
go run ./cmd/server/

# テストの実行
go test -v ./...

# プロダクションビルド
go build -o fuwapachi-server ./cmd/server/
```

---

## 注意事項
- `git commit .` は禁止
- `.env` ファイルをコミットしない（機密情報が含まれるため）
- `main` ブランチへの直接プッシュは禁止。必ず PR を通す
- 1機能1ブランチで機能追加を管理する
- API の仕様を変更した場合は、必ず提供されている Postman コレクションのテスト項目もメンテし、検証を行う
