const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const source = readFileSync(join(__dirname, '../static/upload.js'), 'utf8');

class Element {
  constructor(text = '') {
    this.textContent = text;
    this.hidden = true;
    this.disabled = false;
    this.dataset = {};
    this.listeners = {};
    this.attributes = {};
    this.children = [];
    this.classList = { add() {}, remove() {} };
  }
  addEventListener(type, listener) { (this.listeners[type] ||= []).push(listener); }
  emit(type, event = {}) { this.listeners[type]?.forEach((fn) => fn(event)); }
  setAttribute(name, value) { this.attributes[name] = value; }
  removeAttribute(name) { delete this.attributes[name]; }
  replaceChildren() { this.children = []; }
  append(...children) { this.children.push(...children); }
}

function setup({ valid = true, sendError = false, available = true } = {}) {
  const elements = Object.fromEntries([
    'upload-form', 'upload-feedback', 'upload-status', 'upload-percent',
    'upload-progress', 'upload-details', 'selected-files', 'file-drop',
  ].map((id) => [id, new Element()]));
  const form = elements['upload-form'];
  const button = new Element('上传作品 ↑');
  const file = new Element();
  const title = new Element();
  const article = new Element();
  article.value = '';
  const requestInput = new Element();
  requestInput.value = 'initial-request-id';
  const preDisabled = new Element();
  preDisabled.disabled = true;
  file.files = [{name: 'chosen-photo.png', size: 1024}, {name: 'chosen-video.mp4', size: 2048}];
  const controls = [requestInput, file, title, article, preDisabled, button];
  const requests = [];
  const navigation = [];
  form.action = 'http://localhost/s/first';
  form.dataset.maxUploadMb = '200';
  form.dataset.timeoutMs = '66000';
  form.reportValidity = () => valid;
  form.querySelector = (s) => s.includes('request_id') ? requestInput : s.includes('file') ? file : button;
  form.querySelectorAll = () => controls;

  class Request extends Element {
    constructor() { super(); this.upload = new Element(); this.headers = {}; requests.push(this); }
    open(method, url) { this.method = method; this.url = url; }
    setRequestHeader(name, value) { this.headers[name] = value; }
    send(data) {
      if (sendError) throw new Error('connection failed');
      this.data = data;
    }
  }
  class Multipart {
    constructor() {
      // Disabled fields must still be present in the real submitted body.
      assert.equal(file.disabled, false);
      assert.equal(title.disabled, false);
      assert.equal(requestInput.disabled, false);
      this.id = requestInput.value;
      this.files = [...file.files];
      this.article = article.value;
    }
    get(name) { if (name === 'request_id') return this.id; if (name === 'article') return this.article; }
  }
  let nextID = 0;
  const window = {
    XMLHttpRequest: Request,
    FormData: Multipart,
    DataTransfer: class {},
    crypto: { randomUUID: () => `new-request-${++nextID}` },
    location: { assign: (url) => navigation.push(url) },
  };
  if (!available) delete window.XMLHttpRequest;
  vm.runInNewContext(source, {
    window, XMLHttpRequest: Request, FormData: Multipart,
    document: { getElementById: (id) => elements[id], createElement: () => new Element() },
    DOMParser: class {
      parseFromString(text) {
        return { querySelector: () => ({ textContent: text }) };
      }
    },
  });
  function submit() {
    let prevented = false;
    form.emit('submit', { preventDefault: () => { prevented = true; } });
    return prevented;
  }
  return { elements, form, button, file, title, article, preDisabled, requestInput, requests, navigation, submit };
}

test('real byte progress, duplicate prevention, saving state and confirmed success', () => {
  const ui = setup();
  assert.equal(ui.elements['upload-feedback'].hidden, true);
  assert.equal(ui.submit(), true);
  const request = ui.requests[0];
  assert.equal(request.method, 'POST');
  assert.equal(request.url, ui.form.action);
  assert.equal(request.headers.Accept, 'application/json');
  assert.equal(request.headers['X-Upload-ID'], 'initial-request-id');
  assert.equal(request.timeout, 66000);
  assert.deepEqual(request.data.files, ui.file.files);
  assert.equal(ui.button.disabled, true);
  assert.equal(ui.file.disabled, true);
  assert.equal(ui.form.attributes['aria-busy'], 'true');
  assert.equal(ui.elements['upload-feedback'].hidden, false);
  ui.submit();
  assert.equal(ui.requests.length, 1);

  request.upload.emit('progress', { lengthComputable: true, loaded: 1048576, total: 4194304 });
  assert.equal(ui.elements['upload-progress'].value, 25);
  assert.equal(ui.elements['upload-percent'].textContent, '25%');
  assert.equal(ui.elements['upload-details'].textContent, '1.0 MB / 4.0 MB');
  request.upload.emit('load');
  assert.equal(ui.elements['upload-progress'].value, 100);
  assert.equal(ui.elements['upload-feedback'].dataset.state, 'saving');
  assert.match(ui.elements['upload-status'].textContent, /正在保存/);
  assert.equal(ui.navigation.length, 0);
  assert.equal(ui.button.disabled, true);

  request.status = 200;
  request.responseText = JSON.stringify({ok: true, redirect: 'https://untrusted.example'});
  request.emit('load');
  assert.deepEqual(ui.navigation, [ui.form.action]);
});

test('unknown content length uses an indeterminate progress bar', () => {
  const ui = setup();
  ui.submit();
  ui.requests[0].upload.emit('progress', { lengthComputable: false, loaded: 2048 });
  assert.equal(ui.elements['upload-percent'].textContent, '');
  assert.equal(ui.elements['upload-details'].textContent, '已发送 2.0 KB');
});

test('HTTP errors preserve files, show useful feedback and allow a fresh attempt', () => {
  for (const [code, text] of [[400, '只支持常见图片和视频格式'], [403, '上传时间已结束'], [409, '标识冲突'], [413, ''], [429, ''], [507, ''], [500, '']]) {
    const ui = setup();
    ui.submit();
    const request = ui.requests[0];
    request.status = code;
    request.responseText = text ? JSON.stringify({error:text}) : '<html>proxy error</html>';
    request.emit('load');
    assert.equal(ui.elements['upload-feedback'].dataset.state, 'error');
    assert.equal(ui.button.disabled, false);
    assert.equal(ui.button.textContent, '上传作品 ↑');
    assert.equal(ui.file.disabled, false);
    assert.equal(ui.preDisabled.disabled, true);
    assert.equal(ui.form.attributes['aria-busy'], undefined);
    assert.equal(ui.file.files.length, 2);
    assert.equal(ui.navigation.length, 0);
    if (text) assert.equal(ui.elements['upload-details'].textContent, text);
    if (code === 413) assert.match(ui.elements['upload-details'].textContent, /200 MB/);
    ui.submit();
    assert.equal(ui.requests.length, 2);
    assert.equal(ui.requests[1].headers['X-Upload-ID'], ui.requests[0].headers['X-Upload-ID']);
    assert.equal(ui.elements['upload-feedback'].dataset.state, 'uploading');
    assert.equal(ui.elements['upload-progress'].value, 0);
  }
});

test('network failure, cancellation and timeout restore the form', () => {
  for (const event of ['error', 'abort', 'timeout']) {
    const ui = setup();
    ui.submit();
    ui.requests[0].emit(event);
    assert.equal(ui.elements['upload-feedback'].dataset.state, 'error');
    assert.equal(ui.button.disabled, false);
    assert.equal(ui.navigation.length, 0);
    ui.submit();
    assert.equal(ui.requests.length, 2);
    assert.equal(ui.requests[1].headers['X-Upload-ID'], ui.requests[0].headers['X-Upload-ID']);
  }
});

test('synchronous request failure restores the form', () => {
  const ui = setup({ sendError: true });
  ui.submit();
  assert.equal(ui.button.disabled, false);
  assert.match(ui.elements['upload-details'].textContent, /无法连接服务器/);
});

test('invalid forms and browsers without XHR retain their native behavior', () => {
  const invalid = setup({ valid: false });
  invalid.submit();
  assert.equal(invalid.requests.length, 0);
  const fallback = setup({ available: false });
  assert.equal(fallback.submit(), false);
  assert.equal(fallback.requests.length, 0);
});

test('unexpected HTML or incomplete JSON never discards selected files', () => {
  for (const body of ['<html>login page</html>', '{}', '{"ok":false}']) {
    const ui = setup(); ui.submit();
    Object.assign(ui.requests[0], { status: 200, responseText: body });
    ui.requests[0].emit('load');
    assert.equal(ui.navigation.length, 0);
    assert.equal(ui.button.disabled, false);
    assert.equal(ui.file.files.length, 2);
  }
});

test('file and title changes create new batch IDs and show safe filenames', () => {
  const ui = setup();
  ui.file.files = [{name: '<img onerror=alert(1)>.png', size: 1048576}];
  ui.file.emit('change');
  assert.equal(ui.requestInput.value, 'new-request-1');
  assert.equal(ui.elements['selected-files'].hidden, false);
  const row = ui.elements['selected-files'].children[0];
  assert.equal(row.children[0].textContent, '<img onerror=alert(1)>.png');
  assert.equal(row.children[1].textContent, '1.0 MB');
  ui.form.emit('input');
  assert.equal(ui.requestInput.value, 'new-request-2');
});

test('drag and drop selects files and resets the upload batch', () => {
  const ui = setup();
  ui.elements['file-drop'].emit('drop', {preventDefault() {}, dataTransfer: {files: [{name: 'drop.png', size: 123}]}});
  assert.equal(ui.file.files[0].name, 'drop.png');
  assert.equal(ui.requestInput.value, 'new-request-1');
});

test('excessive file size and count stop before sending the request', () => {
  for (const files of [Array.from({length:101}, () => ({size:1})), [{size:201 * 1024 * 1024}]]) {
    const ui = setup(); ui.file.files = files; ui.submit();
    assert.equal(ui.requests[0].data, undefined);
    assert.equal(ui.button.disabled, false);
    assert.match(ui.elements['upload-details'].textContent, /每次最多 100 个文件/);
  }
});


test('pasted article submits without files and survives a failed request', () => {
  const ui = setup();
  ui.file.files = [];
  ui.article.value = '今天的研学文章\n新的段落';
  ui.submit();
  assert.equal(ui.requests[0].data.article, ui.article.value);
  assert.equal(ui.article.disabled, true);
  ui.requests[0].emit('error');
  assert.equal(ui.article.disabled, false);
  assert.equal(ui.article.value, '今天的研学文章\n新的段落');
  ui.form.emit('input');
  assert.equal(ui.requestInput.value, 'new-request-1');
});

test('empty submissions and oversized pasted articles do not send', () => {
  for (const text of ['', '   ', '字'.repeat(20001)]) {
    const ui = setup();
    ui.file.files = [];
    ui.article.value = text;
    ui.submit();
    assert.equal(ui.requests[0].data, undefined);
    assert.equal(ui.button.disabled, false);
    assert.equal(ui.article.value, text);
    assert.equal(ui.elements['upload-feedback'].dataset.state, 'error');
  }
});
