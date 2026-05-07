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

`migrate/` 以下に MySQL → Firestore の一括移行ツールがあります。MySQL の各行を `plays` に書き、メモリ上で集計した楽曲メタを `songs` に書き出します。

```sh
cd migrate
export DATABASE_URI='user:pass@tcp(host:3306)/dbname'
export PROJECT_ID='your-project-id'
export FIRESTORE_DATABASE='onairlog'
gcloud auth application-default login

# 全件移行
go run .

# 中断時の再開 (出力末尾の lastID から)
go run . --start-id=123456

# 動作確認 (Firestore に書き込まずに正規化結果と件数を見る)
go run . --limit=100 --dry-run

# songs / plays を全消去
go run . --reset
```

ID 生成が決定論的なので、再実行は冪等です。同じ曲が同じ時刻に重複登録されることはありません。
