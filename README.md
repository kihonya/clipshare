# clipshare

iPhone と Windows PC のあいだで、クリップボード（テキストと画像）を共有する小さなプログラムです。通信は Tailscale のネットワーク内だけで行い、クラウドには一切出しません。

## 仕組み

- Windows 側で `clipshare.exe` を起動しておくと、「箱」サーバー兼クリップボードの番人として動きます。
  - iPhone から届いたものを Windows のクリップボードに自動で反映します。
  - Windows でコピーしたものを箱に入れます。
- 箱が保持するのは最新 1 件だけ、中身はメモリ上のみ（ディスクに書きません。再起動で消えます）。
- iPhone 側は Web ページ（`/app`）を使います。トークン付き URL を Safari で開いてホーム画面に追加するだけです。
  - 送る: ページの欄にペーストした瞬間に送信されます。
  - 受け取る: 「受け取る」→「クリップボードにコピー」の 2 タップ（Safari の仕様に合わせた作りです）。
  - 「箱を空にする（クリア）」ボタンもあります（2 回押して初めて消えます）。

## ビルド

Go 標準ライブラリのみで書かれています（外部依存なし）。

```
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o clipshare.exe .
```

コンソール画面を出さない版にする場合は `-H=windowsgui` を足します。

## 使い方

1. `clipshare.exe` を起動します（初回に同じフォルダへ `clipshare.json` が作られ、トークンが生成されます）。
2. ログに表示される `http://<tailscale-ip>:8765/app?k=<token>` を iPhone の Safari で開き、ホーム画面に追加します。
3. 自動起動したい場合は `setup-autostart.bat` を実行すると、スタートアップに登録されて初回起動も済みます。

## 仕様

API の詳細は `API.md`、iPhone 側の手動セットアップ（ショートカット方式）の記録は `MANUAL-iPhone.md` を参照してください。

- 待ち受け: Tailscale の IPv4（100.64.0.0/10）と localhost のみ
- 認証: トークン（`X-Clip-Token` ヘッダーまたは Bearer）
- 上限: テキスト 1MiB、画像 10MiB（画像は PNG に正規化されます）
