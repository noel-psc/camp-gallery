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
  }
  addEventListener(type, listener) { this.listeners[type] = listener; }
  emit(type, event = {}) { this.listeners[type]?.(event); }
  setAttribute(name, value) { this.attributes[name] = value; }
  removeAttribute(name) { delete this.attributes[name]; }
}

function setup({ valid = true, sendError = false, available = true } = {}) {
  const elements = Object.fromEntries([
    'upload-form', 'upload-feedback', 'upload-status', 'upload-percent',
    'upload-progress', 'upload-details',
  ].map((id) => [id, new Element()]));
  const form = elements['upload-form'];
  const button = new Element('上传作品 ↑');
  const file = new Element();
  const title = new Element();
  const preDisabled = new Element();
  preDisabled.disabled = true;
  file.files = ['chosen-photo.png', 'chosen-video.mp4'];
  const controls = [file, title, preDisabled, button];
  const requests = [];
  const navigation = [];
  form.action = 'http://localhost/s/first';
  form.dataset.maxUploadMb = '200';
  form.reportValidity = () => valid;
  form.querySelector = () => button;
  form.querySelectorAll = () => controls;

  class Request extends Element {
    constructor() { super(); this.upload = new Element(); requests.push(this); }
    open(method, url) { this.method = method; this.url = url; }
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
      this.files = [...file.files];
    }
  }
  const window = {
    XMLHttpRequest: Request,
    FormData: Multipart,
    location: { assign: (url) => navigation.push(url) },
  };
  if (!available) delete window.XMLHttpRequest;
  vm.runInNewContext(source, {
    window, XMLHttpRequest: Request, FormData: Multipart,
    document: { getElementById: (id) => elements[id] },
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
  return { elements, form, button, file, title, preDisabled, requests, navigation, submit };
}

test('real byte progress, duplicate prevention, saving state and confirmed success', () => {
  const ui = setup();
  assert.equal(ui.elements['upload-feedback'].hidden, true);
  assert.equal(ui.submit(), true);
  const request = ui.requests[0];
  assert.equal(request.method, 'POST');
  assert.equal(request.url, ui.form.action);
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
  for (const [code, text] of [[400, '只支持常见图片和视频格式'], [403, '上传时间已结束'], [413, ''], [500, '']]) {
    const ui = setup();
    ui.submit();
    const request = ui.requests[0];
    request.status = code;
    request.responseText = text;
    request.emit('load');
    assert.equal(ui.elements['upload-feedback'].dataset.state, 'error');
    assert.equal(ui.button.disabled, false);
    assert.equal(ui.button.textContent, '上传作品 ↑');
    assert.equal(ui.file.disabled, false);
    assert.equal(ui.preDisabled.disabled, true);
    assert.equal(ui.form.attributes['aria-busy'], undefined);
    assert.deepEqual(ui.file.files, ['chosen-photo.png', 'chosen-video.mp4']);
    assert.equal(ui.navigation.length, 0);
    if (text) assert.equal(ui.elements['upload-details'].textContent, text);
    if (code === 413) assert.match(ui.elements['upload-details'].textContent, /200 MB/);
    ui.submit();
    assert.equal(ui.requests.length, 2);
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
