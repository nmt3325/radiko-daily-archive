# Radiko Daily Archive

`github.com/yyoshiki41/go-radiko` を利用し、radiko の1放送日分を GitHub Actions で取得して、Private Repository の Releases に日次保存します。

> **個人・非商用の視聴用途のみで使用してください。** radiko の利用規約、番組の権利、適用法令を守ってください。再配布や公開 Repository での運用を想定していません。

## 動作

- 毎日 **06:15 JST** に起動し、前日の radiko 放送日を処理
- 既定エリアは東京 `JP13`
- エリア内の全局を動的に列挙
- 各局のタイムフリー対象番組を AAC で取得
- 局ごとに `tar.zst` 化し、`radiko-YYYY-MM-DD` Release に保存
- `manifest.json` に番組名、取得成否、サイズ、SHA-256 を記録
- 1ファイルが 2 GiB に近づく場合は自動分割
- 手動実行では、日付・エリア・局を指定してバックフィル可能

radiko の番組日は一般的な暦日と異なり、概ね 05:00 から翌 05:00 までです。そのため翌朝に前日の番組表を処理します。

## 最初に必要な設定

GitHub-hosted runner の通信元は通常日本国外です。そのままでは radiko が `OUT` と判定し、処理できません。次のどちらかを設定してください。

### A. 日本国内の信頼できるプロキシを使う

Repository の **Settings → Secrets and variables → Actions → Secrets** に追加します。

| Secret | 必須条件 | 内容 |
|---|---|---|
| `RADIKO_PROXY_URL` | GitHub-hosted runner を使う場合 | 日本国内出口の HTTP/HTTPS/SOCKS5 プロキシ URL |
| `RADIKO_MAIL` | エリアフリー利用時 | radiko プレミアムのメールアドレス |
| `RADIKO_PASSWORD` | エリアフリー利用時 | radiko プレミアムのパスワード |

プロキシは認証情報を含めて Secret に保存し、信頼できるものだけを使用してください。

### B. 日本国内の self-hosted runner を使う

Repository variable `RADIKO_RUNNER` に self-hosted runner のラベルを設定します。例: `self-hosted`。runner が対象エリア内にあり、そのエリアだけを取得するならプレミアム認証は不要です。

## Repository variables

**Settings → Secrets and variables → Actions → Variables** で変更できます。

| Variable | 既定値 | 説明 |
|---|---:|---|
| `RADIKO_AREA_ID` | `JP13` | 定期実行で取得するエリア |
| `RADIKO_STATION_IDS` | 空 | カンマ区切りの局ID。空ならエリア内全局 |
| `RADIKO_RUNNER` | `ubuntu-latest` | Actions runner ラベル |

例: `RADIKO_STATION_IDS=TBS,QRR,LFR,FMT,FMJ`

## 手動実行

1. **Actions → Radiko daily archive → Run workflow** を開く
2. 必要に応じて以下を入力
   - `date`: `YYYY-MM-DD`（JST、空なら前日）
   - `area`: `JP13` など
   - `stations`: `TBS,QRR,LFR` など。空なら全局
3. 実行後、**Releases** の該当日を確認

## Release の中身

```text
radiko-2026-08-24
├── plan.json
├── 2026-08-24__JP13__TBS.tar.zst
├── 2026-08-24__JP13__TBS.tar.zst.sha256
└── ...
```

局アーカイブの例:

```text
ABOUT.txt
manifest.json
20260824050000--TBS--番組名.aac
20260824060000--TBS--番組名.aac
...
```

タイムフリー非対応、配信停止、権利上の制限、API障害などで取得できない番組は `manifest.json` に失敗理由を残します。成功済みの番組と局アーカイブは Release に保存されます。

## 注意点

- 全局×毎日はデータ量と Actions 実行時間が非常に大きくなります。GitHub の Actions 分数、Release の制限、ネットワーク利用量を確認してください。
- GitHub Release の単一 asset 上限を避けるため、約 1.9 GiB で分割します。
- `go-radiko` は非公式 API クライアントです。radiko 側の仕様変更で停止する可能性があります。
- GitHub の scheduled workflow は混雑時に遅延することがあります。
- Release を公開したり Repository を Public に変更したりしないでください。

## ローカル確認

```bash
make check

export RADIKO_MAIL='...'
export RADIKO_PASSWORD='...'
export HTTPS_PROXY='http://日本国内プロキシ:ポート'
export HTTP_PROXY="$HTTPS_PROXY"

./bin/radiko-archive \
  -mode plan \
  -date 2026-08-24 \
  -area JP13 \
  -plan plan.json
```

## License

GPL-3.0-only. `go-radiko` のライセンスと非商用・個人利用に関する注意も確認してください。
