# Radiko Nationwide Daily Archive

[`yyoshiki41/go-radiko`](https://github.com/yyoshiki41/go-radiko) で全国の番組表を作成し、[`garret1317/yt-dlp-rajiko`](https://github.com/garret1317/yt-dlp-rajiko) で各地域のタイムフリー番組を高速取得します。GitHub Actionsから、日本国内プロキシなしで全国のarea free対象局を扱えます。

> **個人・非商用の視聴用途のみで使用してください。** Repositoryのソースコードは公開しますが、生成した音声は非公開のDraft Releaseだけに保存します。radikoの利用規約、番組の権利、適用法令を守り、音声を公開・再配布しないでください。

## 動作

- 毎日 **06:15 JST** に前日のradiko放送日を処理
- 既定では `JP1`〜`JP47` の全国を対象
- radikoの全国局一覧から `areafree=1` かつ `timefree=1` の局を自動選択
  - 2026年8月時点で101局
  - `areafree=0` のNHK各局は対象外
- `yt-dlp-rajiko` が局の地域を判定し、GitHub-hosted runnerから地域別トークンを取得
- 全番組をM4Aで取得し、局ごとの `tar.zst` として非公開のDraft Releaseへ保存
- `manifest.json` に番組名、ソースURL、取得成否、サイズ、SHA-256を記録
- 単一assetが2 GiBに近づく場合は約1.9 GiBで自動分割
- 日付・エリア・局を指定した手動バックフィルにも対応

radikoの放送日は **05:00〜翌04:59** です。ワークフローは翌朝に前日の放送日を処理します。

## 初期設定

通常の前日分タイムフリー取得には、**Secret・VPN・日本国内プロキシは不要**です。Repositoryには次の設定を投入済みです。

| Variable | 既定値 | 説明 |
|---|---:|---|
| `RADIKO_AREAS` | `all` | 定期実行の対象。`all` または `JP1,JP13,JP27` 形式 |
| `RADIKO_RUNNER` | `ubuntu-latest` | Actions runnerラベル |

局を限定したい場合だけ、Repository variable `RADIKO_STATION_IDS` に `TBS,QRR,LFR,802,NORTHWAVE` のように設定します。

### 任意のSecrets

**Settings → Secrets and variables → Actions → Secrets** で設定できます。

| Secret | 用途 |
|---|---|
| `RADIKO_MAIL` | Timefree30対象期間を手動バックフィルする場合のradikoアカウント |
| `RADIKO_PASSWORD` | 上記アカウントのパスワード |
| `RADIKO_PROXY_URL` | 独自プロキシを明示的に使いたい場合のみ |

`RADIKO_MAIL` と `RADIKO_PASSWORD` は必ず両方設定してください。前日分の日次処理では不要です。

## 手動実行

1. **Actions → Radiko nationwide daily archive → Run workflow** を開く
2. 必要に応じて入力
   - `date`: `YYYY-MM-DD`（JST、空なら前日）
   - `areas`: `all` または `JP1,JP13,JP27`
   - `stations`: `TBS,802,NORTHWAVE`。空なら対象地域の全area-free局
3. 実行後、Repository管理権限のあるアカウントで**Releases → Drafts**の該当日を確認

## 非公開Draft Releaseの中身

```text
radiko-2026-08-24
├── plan.json
├── 2026-08-24__JP1__NORTHWAVE.tar.zst
├── 2026-08-24__JP1__NORTHWAVE.tar.zst.sha256
├── 2026-08-24__JP27__802.tar.zst
└── ...
```

局アーカイブの例:

```text
ABOUT.txt
manifest.json
20260824050000--NORTHWAVE--NORTH_WAVE_CALLIN’.m4a
20260824060000--NORTHWAVE--番組名.m4a
...
```

タイムフリー非対応、配信停止、権利上の制限、API障害などで取得できない番組は `manifest.json` に失敗理由を残します。成功済みの番組と局アーカイブはDraft Releaseに保存されます。

## 重要な注意

- 全国全局では約101 runner jobs/日となり、音声データは数十GB/日になる可能性があります。GitHub ActionsとReleaseの容量・ネットワーク利用量を必ず確認してください。
- `yt-dlp-rajiko` は非公式プラグインです。radiko側の仕様変更で停止する可能性があります。
- NHKは `yt-dlp-rajiko` のradiko extractor対象外で、radiko局一覧でもarea free対象外です。
- GitHubのscheduled workflowは混雑時に遅延することがあります。
- RepositoryのコードとActionsログはPublicです。認証情報や音声データをコード・ログへ出力しないでください。
- GitHubにはPublic Repository用の独立した「Private Release」はないため、**Draft Release**を非公開保存先として使用します。DraftをPublishしないでください。
- WorkflowはReleaseの作成時・更新時に毎回`--draft`を指定し、誤って公開済みになったReleaseも次回処理時にDraftへ戻します。

## バージョン

`requirements.txt` で再現性のため固定しています。

- `yt-dlp 2026.8.19`
- `yt-dlp-rajiko 1.13`

Dependabotが更新候補を週次確認します。

## ローカル確認

```bash
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
make check

./bin/radiko-archive \
  -mode plan \
  -date 2026-08-24 \
  -areas all \
  -plan plan.json

# 北海道と大阪の抽出確認
./bin/radiko-archive \
  -mode plan \
  -date 2026-08-24 \
  -areas JP1,JP27 \
  -stations NORTHWAVE,802 \
  -plan plan.json
```

## License

GPL-3.0-only。依存する `go-radiko` はGPL-3.0、`yt-dlp-rajiko` は0BSDです。各プロジェクトの注意事項も確認してください。
