# onairlog-sync

J-WAVE の OnAir Log をクロールして Firestore に保存し、新着曲を Slack 通知する Cloud Functions。

## データモデル

- `songs/{songId}`: 楽曲マスター。`(title, artist)` を正規化して同一曲を 1 ドキュメントに集約。`firstAired` / `lastAired` / `playCount` を保持。
- `plays/{playId}`: オンエア履歴。各 doc は `songId` で `songs` を参照。`rawTitle` / `rawArtist` には原文を保存。

ID は決定論的: `songId = sha1(normTitle | normArtist)`、`playId = sha1(unix_time | songId)`。同じ曲・同じ時刻のエントリは冪等に書き換えられる。

## 環境変数

| 変数 | 用途 |
|---|---|
| `PROJECT_ID` | GCP プロジェクト ID (Firestore / Pub/Sub / Error Reporting) |
| `FIRESTORE_DATABASE` | Firestore データベース名 (省略時は `(default)`、本番では `onairlog`) |
| `SLACK_WEBHOOK_URL` | Slack Incoming Webhook (Notify 関数のみ) |

## ローカル実行

Cloud Functions Gen 2 では 1 プロセスで 1 関数のみ動かします。`FUNCTION_TARGET` で対象を切り替えてください。

```sh
gcloud auth application-default login

# Sync をローカルで起動
FUNCTION_TARGET=Sync PROJECT_ID=onairlog FIRESTORE_DATABASE=onairlog go run ./local

# 別ターミナルから CloudEvent を投げる
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "ce-id: 1" -H "ce-source: //pubsub.googleapis.com/" \
  -H "ce-specversion: 1.0" \
  -H "ce-type: google.cloud.pubsub.topic.v1.messagePublished" \
  -d '{"message":{"data":""},"subscription":"projects/onairlog/subscriptions/sync"}'

# Notify を試す場合
FUNCTION_TARGET=Notify PROJECT_ID=onairlog FIRESTORE_DATABASE=onairlog \
  SLACK_WEBHOOK_URL=https://... go run ./local
JSON=$(cat fixtures/songs.json)
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "ce-id: 1" -H "ce-source: //pubsub.googleapis.com/" \
  -H "ce-specversion: 1.0" \
  -H "ce-type: google.cloud.pubsub.topic.v1.messagePublished" \
  -d "{\"message\":{\"data\":\"$(echo -n $JSON | base64)\"},\"subscription\":\"projects/onairlog/subscriptions/notify\"}"
```

## Firestore 初期セットアップ

1. データベース作成 (リージョン: `asia-northeast1`、名前: `onairlog`)
   ```sh
   gcloud firestore databases create \
     --database=onairlog \
     --location=asia-northeast1 \
     --type=firestore-native
   ```
   既存の `(default)` DB が Datastore モードで使われている場合に備え、別名で作成します。
2. インデックス設定の反映 (`title`, `artist` の単一フィールドインデックスを無効化してストレージ削減)
   ```sh
   firebase deploy --only firestore:indexes --project=onairlog
   ```
   または `gcloud firestore indexes fields update` で個別に無効化。

## Cloud SQL からのデータ移行

`migrate/` 以下に MySQL → Firestore の一括移行ツールがあります。**集計を MySQL に任せて Firestore に流す 2 段構成**で、メモリ使用量を一定に保ちつつ各フェーズが独立に再開できます。

### Phase 1: 既存 raw `songs` から MySQL 上に `csongs` / `plays` を構築

正規化と aggregation を MySQL の `INSERT ... ON DUPLICATE KEY UPDATE` で行います。

```sh
cd migrate
export DATABASE_URI='user:pass@tcp(host:3306)/dbname'
gcloud auth application-default login

# 全件 prep
go run . --prep

# 再開 (raw songs.id ベース)
go run . --prep --start-id=1234567

# ドライラン
go run . --prep --limit=100 --dry-run
```

完了後、SQL で内容確認:

```sql
SELECT COUNT(*) FROM csongs;
SELECT COUNT(*) FROM plays;
SELECT title, artist, play_count FROM csongs ORDER BY play_count DESC LIMIT 50;
```

### Phase 2: MySQL から Firestore へ流し込み

```sh
export PROJECT_ID='your-project-id'
export FIRESTORE_DATABASE='onairlog'

# 全 plays + csongs を Firestore に投入
go run .

# plays だけ途中再開
go run . --start-play-id=<lastID>

# csongs だけ途中再開
go run . --start-song-id=<lastID>
```

Firestore 側を全消去する場合: `go run . --reset`

ID は決定論的に生成されるため、再実行は冪等です。
