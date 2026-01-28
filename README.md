# Fuwapachi API Server

Goで構築されたリアルタイムメッセージングAPIサーバー。REST APIとWebSocketをサポートし、MariaDB/MySQLをデータベースとして使用します。

## 📋 目次

- [概要](#概要)
- [機能](#機能)
- [技術スタック](#技術スタック)
- [前提条件](#前提条件)
- [セットアップ](#セットアップ)
- [環境変数](#環境変数)
- [API仕様](#api仕様)
- [WebSocket仕様](#websocket仕様)
- [データベーススキーマ](#データベーススキーマ)
- [使用例](#使用例)
- [開発](#開発)

## 概要

Fuwapachi API Serverは、メッセージの作成、取得、削除をサポートするRESTful APIサーバーです。削除イベントはWebSocketを通じてリアルタイムに接続中のクライアントに通知されます。

## 機能

- ✅ **メッセージのCRUD操作** - RESTful APIによるメッセージ管理
- ✅ **リアルタイム通知** - WebSocketによる削除イベントのブロードキャスト
- ✅ **ソフトデリート** - `deleted_at`タイムスタンプによる論理削除
- ✅ **CORS対応** - クロスオリジンリクエストのサポート
- ✅ **環境変数管理** - `.env`ファイルによる設定管理
- ✅ **詳細なログ** - すべてのAPI操作とWebSocketイベントをログ出力

## 技術スタック

- **言語**: Go
- **Webフレームワーク**: [Gorilla Mux](https://github.com/gorilla/mux)
- **WebSocket**: [Gorilla WebSocket](https://github.com/gorilla/websocket)
- **データベース**: MariaDB/MySQL
- **データベースドライバ**: [go-sql-driver/mysql](https://github.com/go-sql-driver/mysql)
- **CORS**: [rs/cors](https://github.com/rs/cors)
- **環境変数**: [godotenv](https://github.com/joho/godotenv)

## 前提条件

- Go 1.16以上
- MariaDB 10.x または MySQL 5.7以上
- 開発環境用のフロントエンドアプリケーション（オプション）

## セットアップ

### 1. リポジトリのクローン

```bash
git clone <repository-url>
cd api_fuwapachi
```

### 2. 依存関係のインストール

```bash
go mod download
```

### 3. データベースの準備

MariaDB/MySQLに接続し、以下のSQLを実行してデータベースとテーブルを作成します：

```sql
CREATE DATABASE IF NOT EXISTS fuwapachi;
USE fuwapachi;

CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(255) PRIMARY KEY,
    content TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_deleted_at (deleted_at)
);
```

### 4. 環境変数の設定

プロジェクトルートに `.env` ファイルを作成します：

```bash
# データベース設定
DB_HOST=localhost
DB_PORT=3306
DB_USER=your_db_user
DB_PASSWORD=your_db_password
DB_NAME=fuwapachi

# サーバー設定
SERVER_PORT=8080
ENV=development

# CORS設定
ALLOWED_ORIGINS=http://localhost:3000,http://127.0.0.1:3000
```

### 5. サーバーの起動

```bash
go run main.go
```

サーバーが起動すると、以下のような出力が表示されます：

```
========================================
  Fuwapachi API Server
========================================
  Environment: development
  Server: http://localhost:8080
  WebSocket: ws://localhost:8080/ws
  Database: user@localhost:3306/fuwapachi
  Allowed Origins: [http://localhost:3000 http://127.0.0.1:3000]
========================================
✅ Database connection established
🚀 Server started successfully
```

## 環境変数

| 変数名 | 説明 | デフォルト値 |
|--------|------|-------------|
| `DB_HOST` | データベースホスト | `localhost` |
| `DB_PORT` | データベースポート | `3306` |
| `DB_USER` | データベースユーザー | - |
| `DB_PASSWORD` | データベースパスワード | - |
| `DB_NAME` | データベース名 | - |
| `SERVER_PORT` | サーバーポート | `8080` |
| `ENV` | 環境 (development/production) | `development` |
| `ALLOWED_ORIGINS` | CORS許可オリジン（カンマ区切り） | `http://localhost:3000,http://127.0.0.1:3000` |

## API仕様

### ベースURL

```
http://localhost:8080
```

### エンドポイント

#### 1. メッセージ一覧の取得

```http
GET /messages
```

**レスポンス**

```json
[
  {
    "id": "msg_123",
    "content": "Hello, World!",
    "created_at": "2026-01-29T12:00:00Z",
    "deleted_at": null
  },
  {
    "id": "msg_456",
    "content": "Deleted message",
    "created_at": "2026-01-29T11:00:00Z",
    "deleted_at": "2026-01-29T11:30:00Z"
  }
]
```

#### 2. メッセージの作成

```http
POST /messages
Content-Type: application/json
```

**リクエストボディ**

```json
{
  "content": "New message"
}
```

**レスポンス** (201 Created)

```json
{
  "id": "789",
  "content": "New message",
  "created_at": "2026-01-29T12:30:00Z",
  "deleted_at": null
}
```

**エラーレスポンス**

- `400 Bad Request`: contentが欠落または空の場合
- `500 Internal Server Error`: データベースエラー

#### 3. メッセージの削除

```http
DELETE /messages/{id}
```

**レスポンス** (204 No Content)

削除が成功した場合、レスポンスボディはありません。`deleted_at`フィールドが現在時刻で更新されます。

**エラーレスポンス**

- `404 Not Found`: 指定されたIDのメッセージが存在しない
- `500 Internal Server Error`: データベースエラー

**副作用**: 削除が成功すると、WebSocket経由で接続中のすべてのクライアントに削除イベントが通知されます。

## WebSocket仕様

### 接続エンドポイント

```
ws://localhost:8080/ws
```

### 接続要件

- `Origin`ヘッダーが`ALLOWED_ORIGINS`環境変数で指定されたオリジンと一致する必要があります
- WebSocketプロトコルを使用

### イベント

#### 削除イベント

メッセージが削除されると、サーバーは以下の形式のJSONをすべての接続クライアントにブロードキャストします：

```json
{
  "type": "message_deleted",
  "id": "msg_789",
  "deleted_at": "2026-01-29T12:45:00Z"
}
```

### 使用例 (JavaScript)

```javascript
const ws = new WebSocket('ws://localhost:8080/ws');

ws.onopen = () => {
  console.log('WebSocket connected');
};

ws.onmessage = (event) => {
  const data = JSON.parse(event.data);
  if (data.type === 'message_deleted') {
    console.log(`Message ${data.id} was deleted at ${data.deleted_at}`);
    // UIからメッセージを削除または更新
  }
};

ws.onerror = (error) => {
  console.error('WebSocket error:', error);
};

ws.onclose = () => {
  console.log('WebSocket disconnected');
};
```

## データベーススキーマ

### `messages` テーブル

| カラム名 | 型 | 制約 | 説明 |
|----------|-----|------|------|
| `id` | VARCHAR(255) | PRIMARY KEY | メッセージの一意識別子 |
| `content` | TEXT | NOT NULL | メッセージの内容 |
| `created_at` | DATETIME | NOT NULL | 作成日時 |
| `deleted_at` | DATETIME | NULL | 削除日時（NULL = 削除されていない） |

**インデックス**
- `idx_deleted_at`: `deleted_at`カラムにインデックスを作成し、削除されたメッセージのクエリを高速化

## 使用例

### cURLを使用したAPI呼び出し

#### メッセージの作成

```bash
curl -X POST http://localhost:8080/messages \
  -H "Content-Type: application/json" \
  -d '{"id": "msg_001", "content": "Hello from cURL!"}'
```

#### メッセージの取得

```bash
curl http://localhost:8080/messages
```

#### メッセージの削除

```bash
curl -X DELETE http://localhost:8080/messages/msg_001
```

## 開発

### テストの実行

```bash
go test -v
```

### Postmanでのテスト

プロジェクトには包括的なPostmanコレクションが含まれています。

#### インポート手順

1. Postmanを開く
2. 左上の「Import」ボタンをクリック
3. 以下のファイルをドラッグ&ドロップまたは選択：
   - `Fuwapachi_API.postman_collection.json` - APIテストコレクション
   - `Fuwapachi_API.postman_environment.json` - 環境変数設定

#### テストの実行

**個別テストの実行：**
1. コレクション内の任意のリクエストを選択
2. 「Send」ボタンをクリック
3. 「Tests」タブで自動テスト結果を確認

**コレクション全体の実行：**
1. コレクション名の横の「...」をクリック
2. 「Run collection」を選択
3. Collection Runnerで「Run Fuwapachi API」をクリック
4. すべてのテストが自動実行され、結果が表示されます

#### テストカテゴリ

**Messages（基本操作）：**
- ✅ Get All Messages - メッセージ一覧取得
- ✅ Create Message - メッセージ作成
- ✅ Delete Message - メッセージ削除

**Error Cases（エラーハンドリング）：**
- ❌ Create Message - Missing ID - ID欠落エラー
- ❌ Create Message - Duplicate ID - 重複IDエラー
- ❌ Create Message - Invalid JSON - 不正なJSON
- ❌ Delete Message - Not Found - 存在しないメッセージ

**Integration Tests（統合テスト）：**
- 🔄 Full Workflow - 作成→取得→削除→確認の完全フロー

#### 自動テスト機能

各リクエストには以下の自動検証が含まれています：

- ステータスコードの検証
- レスポンス形式の検証
- データ構造の検証
- レスポンスタイムの検証
- エラーメッセージの検証

### ビルド

```bash
go build -o fuwapachi-server main.go
```

### 実行

```bash
./fuwapachi-server
```

## アーキテクチャ

### コンポーネント構成

```
┌─────────────┐
│  クライアント  │
│  (Browser)   │
└──────┬──────┘
       │ HTTP/WebSocket
       │
┌──────▼──────────────────┐
│  Fuwapachi API Server    │
│  - REST API              │
│  - WebSocket Handler     │
│  - CORS Middleware       │
└──────┬──────────────────┘
       │ SQL
       │
┌──────▼──────┐
│  MariaDB/   │
│  MySQL      │
└─────────────┘
```

### WebSocketブロードキャストフロー

1. クライアントA、B、Cがサーバーに接続
2. クライアントAが`DELETE /messages/{id}`をリクエスト
3. サーバーがデータベースの`deleted_at`を更新
4. 削除イベントが`broadcast`チャネルに送信
5. `handleBroadcast`ゴルーチンがイベントを受信
6. すべての接続クライアント（B、C）にイベントがブロードキャスト
7. クライアントB、CがUIを更新

### 並行処理の安全性

- **WebSocket接続管理**: `sync.RWMutex`を使用して`clients`マップへの並行アクセスを保護
- **ブロードキャストチャネル**: バッファサイズ100のチャネルを使用し、DELETEリクエストのブロッキングを回避
- **スナップショット方式**: ブロードキャスト時にクライアントリストのスナップショットを作成し、イテレーション中のマップ更新を防止

## ログ

サーバーはすべての操作を詳細にログ出力します：

- ✅ 成功した操作
- ❌ エラー
- 📢 WebSocketブロードキャスト
- 🚀 サーバー起動

例：

```
[POST /messages] Request received from 127.0.0.1:54321
[POST /messages] ✅ Created message: ID=msg_123, Content="Hello"
[DELETE /messages/msg_123] Request received from 127.0.0.1:54322
[DELETE /messages/msg_123] ✅ Deleted successfully
[WebSocket] 📢 Broadcasting delete event for message: msg_123
```

## ライセンス

このプロジェクトのライセンスについては、プロジェクト管理者にお問い合わせください。

## 貢献

プルリクエストやイシューの報告を歓迎します。大きな変更を行う場合は、まずイシューを開いて変更内容を議論してください。
