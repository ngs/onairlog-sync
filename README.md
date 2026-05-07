# onairlog-sync

J-WAVE の OnAir Log をクロールして Firestore に保存し、新着曲を Slack 通知する Cloud Functions。

## 環境変数

| 変数 | 用途 |
|---|---|
| `PROJECT_ID` | GCP プロジェクト ID (Firestore / Pub/Sub / Error Reporting) |
| `SLACK_WEBHOOK_URL` | Slack Incoming Webhook (Notify 関数のみ) |

## ローカル実行

```sh
gcloud auth application-default login

go run ./local

# 別ターミナル
curl http://localhost:8080/sync -d '{"data":""}'
JSON=$(cat fixtures/songs.json)
curl http://localhost:8080/notify -d "{\"data\":\"$(echo $JSON | base64)\"}"
```

## Firestore 初期セットアップ

1. データベース作成 (リージョン: `asia-northeast1`)
   ```sh
   gcloud firestore databases create --location=asia-northeast1 --type=firestore-native
   ```
2. インデックス設定の反映 (`title`, `artist` の単一フィールドインデックスを無効化してストレージ削減)
   ```sh
   firebase deploy --only firestore:indexes
   ```
   または `gcloud firestore indexes fields update` で個別に無効化。

## Cloud SQL からのデータ移行

`migrate/` 以下に MySQL → Firestore の一括移行ツールがあります。

```sh
cd migrate
export DATABASE_URI='user:pass@tcp(host:3306)/dbname'
export PROJECT_ID='your-project-id'
gcloud auth application-default login

# 全件移行
go run .

# 中断時の再開 (出力末尾の lastID から)
go run . --start-id=123456

# 動作確認
go run . --limit=100 --dry-run
```

ドキュメント ID は `sha1(unix_time | title | artist)` で決定論的に生成されるため、再実行は冪等です。
