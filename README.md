# zip2win

Windows の標準機能（エクスプローラー）で展開しても日本語ファイル名が文字化けしない ZIP を作る CLI。
macOS / Linux / Windows で動く。

## インストール

```bash
go install ./cmd/zip2win
```

## 使い方

```bash
# ディレクトリ（またはファイル）を ZIP にする。docs/ の中身は docs/... として格納される
zip2win create docs docs.zip

# 既存の出力先を上書きする
zip2win create --force docs docs.zip

# ZIP を検査する（全エントリ OK なら exit 0、問題があれば exit 1）
zip2win inspect docs.zip
```

フラグは位置引数より前に置く。

## 何をしているか

| 項目 | 方針 |
|---|---|
| ファイル名 | UTF-8 で格納し、EFS フラグ (General Purpose Bit Flag bit 11) を全エントリで明示 |
| Unicode 正規化 | NFC（macOS 由来の NFD 名を変換） |
| パス区切り | `/` |
| 除外 | `.DS_Store`、`._*`、`__MACOSX/`、シンボリックリンク（スキップ時に stderr へ通知。`<source>` 自体が symlink の場合はエラー） |
| NFC 正規化後に名前が衝突 | エラー（同一パスのエントリが複数ある ZIP を作らない） |
| バックスラッシュを含む名前 | 警告を出して続行。`inspect` は NG として報告 |
| 出力先が既存 | エラー。`--force` で上書き（出力先がディレクトリの場合は `--force` でもエラー） |
| 書き込み | 一時ファイル `<出力先>.tmp`（例: `docs.zip` → `docs.zip.tmp`）に書いて完成後に rename。途中失敗で壊れた ZIP を残さない |
| 出力 ZIP が入力ディレクトリ内 | 自分自身は取り込まない |

Windows 予約名（`CON` など）や Windows で使えない文字（`: * ? " < > |`）は検査・変換しない。
展開環境が Windows とは限らないため。

## inspect が報告する問題

- EFS フラグなし、UTF-8 として不正な名前
- NFC 正規化されていない名前（NFD のまま）
- `..` を含むパス、絶対パス、バックスラッシュ区切り（解凍側のパストラバーサル素材）
- macOS メタデータファイルの混入
- 重複するエントリ名（NFC 正規化して初めて同名になる組も報告する）

表示時、エントリ名の制御文字はエスケープされる（端末エスケープシーケンス注入対策）。

## Windows 実機での検証手順

EFS が正しくても、パス長・権限・セキュリティ製品などで展開に失敗することはある。
納品前は対象と同等の Windows 環境で必ず実展開する。

1. Windows 向けにビルドする
   ```bash
   GOOS=windows GOARCH=amd64 go build -o zip2win.exe ./cmd/zip2win
   ```
2. 検証用フォルダを用意する（ASCII 名、日本語名、スペース入り、空フォルダ、深い階層を含める）
3. `zip2win.exe create <folder> test.zip` で ZIP を作る
4. `zip2win.exe inspect test.zip` が exit 0 になることを確認する
5. エクスプローラーで `test.zip` を右クリック →「すべて展開」
6. 展開後のファイル名が文字化けしていないこと、空フォルダが再現されていることを確認する

## 既知の限界

- 出力先の存在チェックと rename の間に理論上の TOCTOU 窓がある（単一ユーザーの CLI として許容）
- あらゆる Windows 環境・古い ZIP ソフトとの 100% 互換は保証しない
