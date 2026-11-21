package main

import "net/http"

// Web page served by clipshare itself (GET / and GET /app).
// The page carries no secrets: the token reaches it via the ?k= query
// parameter of the URL the user opens / adds to the home screen, and the
// page passes it to the API as a header, same as the Shortcuts did.

const pageHTML = `<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-title" content="クリップ共有">
<title>クリップ共有</title>
<style>
  :root { color-scheme: light dark; }
  body { font-family: -apple-system, sans-serif; margin: 0; padding: 16px;
         background: #f2f2f7; color: #111; }
  @media (prefers-color-scheme: dark) {
    body { background: #111; color: #eee; }
    .card { background: #1e1e22 !important; }
    #pastebox { background: #2a2a30 !important; color: #eee; }
  }
  h1 { font-size: 20px; margin: 4px 0 12px; }
  .card { background: #fff; border-radius: 14px; padding: 14px; margin-bottom: 14px; }
  .card h2 { font-size: 16px; margin: 0 0 10px; }
  button { font-size: 17px; padding: 12px 16px; border: 0; border-radius: 10px;
           background: #0a64d8; color: #fff; width: 100%; margin: 6px 0; }
  button.sub { background: #555; }
  button.danger { background: #b03a2e; }
  #pastebox { border: 2px dashed #999; border-radius: 10px; min-height: 64px;
              padding: 10px; font-size: 16px; background: #fafafa; }
  #status { min-height: 22px; font-size: 15px; margin: 8px 2px; }
  img.recv { max-width: 100%; border-radius: 8px; }
  pre.recv { white-space: pre-wrap; word-break: break-all; background: rgba(127,127,127,.15);
             padding: 10px; border-radius: 8px; }
  .hint { font-size: 13px; opacity: .75; }
  input[type=file] { font-size: 15px; margin: 8px 0; }
</style>
</head>
<body>
<h1>クリップ共有</h1>
<div id="status"></div>

<div class="card">
  <h2>Windowsへ送る</h2>
  <button id="sendText">クリップボードのテキストを送る</button>
  <div id="pastebox" contenteditable="true">ここをタップして、ペーストしてね（テキストでも画像でもOK）</div>
  <p class="hint">ボタンで読めないときは、上の欄にペーストすれば送れるよ。ペーストした瞬間に送信されるよ。</p>
  <label class="hint">写真から選んで送る</label><br>
  <input type="file" id="filepick" accept="image/*">
</div>

<div class="card">
  <h2>Windowsから受け取る</h2>
  <button id="recv">箱の中身を受け取る</button>
  <div id="recvarea"></div>
  <p class="hint">受け取ると中身が表示されるよ。そのあと「クリップボードにコピー」を押すと入るよ。Safariの決まりで、コピーはタップした瞬間にやる必要があるから2タップ式だよ。</p>
</div>

<div class="card">
  <h2>箱を空にする</h2>
  <button id="clear" class="danger">箱を空にする（クリア）</button>
  <p class="hint">共有の箱の中身を消すよ。誤って押しても大変にならないよう、2回押すまでは消えないよ。Windowsのクリップボード自体は消えないよ。</p>
</div>

<script>
var token = new URLSearchParams(location.search).get('k') || '';
var statusEl = document.getElementById('status');
function say(m) { statusEl.textContent = m; }
function headers() { return { 'X-Clip-Token': token, 'Content-Type': 'application/json' }; }
function bufToB64(buf) {
  var bytes = new Uint8Array(buf), s = '', chunk = 0x8000;
  for (var i = 0; i < bytes.length; i += chunk) {
    s += String.fromCharCode.apply(null, bytes.subarray(i, i + chunk));
  }
  return btoa(s);
}
function postClip(type, data, mime) {
  say('送っているよ…');
  return fetch('/clip', { method: 'POST', headers: headers(),
    body: JSON.stringify({ type: type, data: data, mime: mime, origin: 'iphone' }) })
  .then(function(r) {
    if (r.ok) { say('送ったよ。Windowsでペーストできるよ。'); }
    else { say('送れなかったよ（エラー ' + r.status + '）。トークン付きのURLで開いているか確認してね。'); }
  }).catch(function(e) { say('送れなかったよ: ' + e); });
}
document.getElementById('sendText').addEventListener('click', function() {
  if (!navigator.clipboard || !navigator.clipboard.readText) {
    say('この環境では直接読めないよ。下の欄にペーストしてね。'); return;
  }
  navigator.clipboard.readText().then(function(t) {
    if (t) { postClip('text', t, 'text/plain'); }
    else { say('クリップボードが空か、テキストではないよ。下の欄にペーストでも送れるよ。'); }
  }).catch(function() {
    say('直接読めなかったよ。下の欄をタップしてペーストしてね。');
    document.getElementById('pastebox').focus();
  });
});
var pastebox = document.getElementById('pastebox');
pastebox.addEventListener('paste', function(e) {
  e.preventDefault();
  var dt = e.clipboardData;
  if (!dt) return;
  var items = dt.items || [];
  for (var i = 0; i < items.length; i++) {
    if (items[i].type && items[i].type.indexOf('image/') === 0) {
      var f = items[i].getAsFile();
      if (f) {
        f.arrayBuffer().then(function(buf) { postClip('image', bufToB64(buf), 'image/png'); });
        pastebox.textContent = '';
        return;
      }
    }
  }
  var t = dt.getData('text/plain');
  if (t) { postClip('text', t, 'text/plain'); }
  pastebox.textContent = '';
});
document.getElementById('filepick').addEventListener('change', function(e) {
  var f = e.target.files && e.target.files[0];
  if (!f) return;
  f.arrayBuffer().then(function(buf) { postClip('image', bufToB64(buf), 'image/png'); });
});
var pendingText = null;
var pendingImageBlob = null;
// Legacy synchronous copy: works inside the tap handler even on plain
// http pages, where the Async Clipboard API is unavailable (Safari
// requires a secure context for navigator.clipboard).
function copyByExecCommand(t) {
  try {
    var ta = document.createElement('textarea');
    ta.value = t;
    ta.style.position = 'fixed';
    ta.style.top = '0';
    ta.style.fontSize = '16px';
    ta.style.opacity = '0.01';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, t.length);
    var ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return !!ok;
  } catch (e) { return false; }
}
document.getElementById('recv').addEventListener('click', function() {
  say('取りに行っているよ…');
  pendingText = null; pendingImageBlob = null;
  fetch('/clip', { headers: { 'X-Clip-Token': token } }).then(function(r) {
    if (r.status === 404) { say('箱は空だよ。Windowsで何かコピーしてから押してね。'); return null; }
    if (!r.ok) { say('受け取れなかったよ（エラー ' + r.status + '）。'); return null; }
    return r.json();
  }).then(function(j) {
    if (!j) return;
    var area = document.getElementById('recvarea');
    area.innerHTML = '';
    var btn = document.createElement('button');
    btn.textContent = 'クリップボードにコピー';
    btn.addEventListener('click', function() {
      // Clipboard writes must happen inside the tap handler itself on
      // iOS Safari, so the data was fetched beforehand and is copied here.
      if (pendingText !== null) {
        if (copyByExecCommand(pendingText)) {
          say('コピーしたよ。貼りたいアプリでペーストしてね。');
        } else if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(pendingText).then(function() {
            say('コピーしたよ。貼りたいアプリでペーストしてね。');
          }).catch(function() {
            say('ボタンではコピーできなかったよ。下の文章を長押しでコピーしてね。');
          });
        } else {
          say('ボタンではコピーできなかったよ。下の文章を長押しでコピーしてね。');
        }
      } else if (pendingImageBlob) {
        try {
          var item = {};
          item['image/png'] = pendingImageBlob;
          navigator.clipboard.write([new ClipboardItem(item)]).then(function() {
            say('画像をコピーしたよ。');
          }).catch(function() {
            say('ボタンではコピーできなかったよ。画像を長押しして「コピー」してね。');
          });
        } catch (e) {
          say('画像を長押しして「コピー」してね。');
        }
      }
    });
    if (j.type === 'text') {
      pendingText = j.data;
      var pre = document.createElement('pre');
      pre.className = 'recv'; pre.textContent = j.data;
      area.appendChild(pre);
      area.appendChild(btn);
      say('受け取ったよ。「クリップボードにコピー」を押してね。');
    } else if (j.type === 'image') {
      var bin = atob(j.data);
      var arr = new Uint8Array(bin.length);
      for (var i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
      pendingImageBlob = new Blob([arr], { type: 'image/png' });
      var img = document.createElement('img');
      img.className = 'recv';
      img.src = 'data:image/png;base64,' + j.data;
      area.appendChild(img);
      area.appendChild(btn);
      say('画像を受け取ったよ。「クリップボードにコピー」を押してね。');
    }
  }).catch(function(e) { say('受け取れなかったよ: ' + e); });
});
var clearArmed = false;
var clearTimer = null;
document.getElementById('clear').addEventListener('click', function() {
  var btn = this;
  if (!clearArmed) {
    clearArmed = true;
    btn.textContent = 'もう一度押すと空になるよ';
    say('消すなら、もう一度ボタンを押してね。やめるときは5秒待ってね。');
    clearTimer = setTimeout(function() {
      clearArmed = false;
      btn.textContent = '箱を空にする（クリア）';
    }, 5000);
    return;
  }
  clearTimeout(clearTimer);
  clearArmed = false;
  btn.textContent = '箱を空にする（クリア）';
  fetch('/clip', { method: 'DELETE', headers: { 'X-Clip-Token': token } }).then(function(r) {
    if (r.ok) {
      pendingText = null;
      pendingImageBlob = null;
      document.getElementById('recvarea').innerHTML = '';
      say('箱を空にしたよ。');
    } else {
      say('消せなかったよ（エラー ' + r.status + '）。');
    }
  }).catch(function(e) { say('消せなかったよ: ' + e); });
});
if (!token) { say('トークン付きのURLで開いてね（?k=... が必要だよ）。'); }
</script>
</body>
</html>
`

func pageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(pageHTML))
}
