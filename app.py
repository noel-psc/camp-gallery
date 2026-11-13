import os
import csv
import io
import re
import secrets
import sqlite3
import tempfile
import uuid
import zipfile
from datetime import datetime
from contextlib import contextmanager
from functools import wraps
from pathlib import Path
from zoneinfo import ZoneInfo

import qrcode
from flask import Flask, abort, redirect, render_template, request, send_file, session, url_for

app = Flask(__name__)
app.secret_key = os.environ.get('SECRET_KEY', 'local-development-only-change-me')
app.config['MAX_CONTENT_LENGTH'] = int(os.environ.get('MAX_UPLOAD_MB', '200')) * 1024 * 1024
DATA = Path(os.environ.get('DATA_DIR', 'data'))
DATA.mkdir(parents=True, exist_ok=True)
UPLOADS = DATA / 'uploads'
UPLOADS.mkdir(exist_ok=True)
DB = DATA / 'camp.sqlite3'
TZ = ZoneInfo('Asia/Shanghai')
DEADLINE = os.environ.get('UPLOAD_DEADLINE', '2026-11-19T23:59:59+08:00')
ALLOWED = {'jpg', 'jpeg', 'png', 'gif', 'webp', 'mp4', 'webm', 'mov'}


@contextmanager
def db():
    conn = sqlite3.connect(DB, timeout=30)
    conn.row_factory = sqlite3.Row
    try:
        with conn:
            yield conn
    finally:
        conn.close()


def init_db():
    with db() as conn:
        conn.executescript('CREATE TABLE IF NOT EXISTS students (id INTEGER PRIMARY KEY, class_name TEXT NOT NULL, name TEXT NOT NULL, token TEXT UNIQUE NOT NULL); CREATE TABLE IF NOT EXISTS works (id INTEGER PRIMARY KEY, student_id INTEGER NOT NULL, filename TEXT NOT NULL, original_name TEXT NOT NULL, title TEXT NOT NULL, created_at TEXT NOT NULL);')
        columns = {row['name'] for row in conn.execute('PRAGMA table_info(students)')}
        if 'student_number' not in columns:
            conn.execute("ALTER TABLE students ADD COLUMN student_number TEXT NOT NULL DEFAULT ''")


def editable():
    return datetime.now(TZ) <= datetime.fromisoformat(DEADLINE).astimezone(TZ)


def student(token):
    with db() as conn:
        row = conn.execute('SELECT * FROM students WHERE token=?', (token,)).fetchone()
    if not row:
        abort(404)
    return row


def admin_required(fn):
    @wraps(fn)
    def wrapped(*args, **kwargs):
        if not session.get('admin'):
            return redirect(url_for('login'))
        return fn(*args, **kwargs)
    return wrapped


@app.route('/')
def home():
    return render_template('home.html')


@app.route('/s/<token>', methods=['GET', 'POST'])
def space(token):
    child = student(token)
    if request.method == 'POST':
        if not editable():
            abort(403, '上传时间已结束')
        name = request.form.get('name', child['name']).strip()
        number = request.form.get('student_number', child['student_number']).strip()
        if name != child['name']:
            abort(400, '请填写与此空间对应的学生姓名')
        if not re.fullmatch(r'[A-Za-z0-9-]{1,32}', number):
            abort(400, '请填写学号（1–32 位数字、英文字母或连字符）')
        files = request.files.getlist('files')
        if not files or all(not f.filename for f in files):
            abort(400, '请选择作品')
        for f in files:
            if not f.filename:
                continue
            ext = f.filename.rsplit('.', 1)[-1].lower()
            if ext not in ALLOWED:
                abort(400, '只支持常见图片和视频格式')
        with db() as conn:
            conn.execute('BEGIN IMMEDIATE')
            current = conn.execute('SELECT student_number FROM students WHERE id=?', (child['id'],)).fetchone()
            if current['student_number'] and current['student_number'] != number:
                abort(400, '学号与此空间已登记的学号不一致，请联系老师核对')
            duplicate = conn.execute('SELECT id FROM students WHERE class_name=? AND student_number=? AND id!=?', (child['class_name'], number, child['id'])).fetchone()
            if duplicate:
                abort(400, '该学号已在本班其他空间登记，请联系老师核对')
            conn.execute('UPDATE students SET student_number=? WHERE id=?', (number, child['id']))
        for f in files:
            if not f.filename:
                continue
            ext = f.filename.rsplit('.', 1)[-1].lower()
            filename = uuid.uuid4().hex + '.' + ext
            f.save(UPLOADS / filename)
            with db() as conn:
                conn.execute('INSERT INTO works (student_id, filename, original_name, title, created_at) VALUES (?, ?, ?, ?, ?)', (child['id'], filename, f.filename[:255], request.form.get('title', '').strip()[:100] or f.filename[:100], datetime.now(TZ).isoformat()))
        return redirect(url_for('space', token=token))
    with db() as conn:
        works = conn.execute('SELECT * FROM works WHERE student_id=? ORDER BY id DESC', (child['id'],)).fetchall()
    can_edit = editable()
    editing = can_edit and (not works or request.args.get('edit') == '1')
    return render_template('space.html', child=child, works=works, editable=can_edit, editing=editing, deadline=datetime.fromisoformat(DEADLINE).astimezone(TZ).strftime('%Y年%m月%d日 %H:%M'))


@app.route('/s/<token>/file/<int:work_id>')
def media(token, work_id):
    child = student(token)
    with db() as conn:
        work = conn.execute('SELECT * FROM works WHERE id=? AND student_id=?', (work_id, child['id'])).fetchone()
    if not work:
        abort(404)
    response = send_file(UPLOADS / work['filename'], conditional=True)
    response.headers['X-Content-Type-Options'] = 'nosniff'
    return response


@app.post('/s/<token>/work/<int:work_id>')
def edit_work(token, work_id):
    child = student(token)
    if not editable():
        abort(403, '修改时间已结束')
    with db() as conn:
        work = conn.execute('SELECT * FROM works WHERE id=? AND student_id=?', (work_id, child['id'])).fetchone()
        if not work:
            abort(404)
        if request.form.get('action') == 'delete':
            conn.execute('DELETE FROM works WHERE id=?', (work_id,))
        else:
            title = request.form.get('title', '').strip()[:100]
            if not title:
                abort(400, '作品名称不能为空')
            conn.execute('UPDATE works SET title=? WHERE id=?', (title, work_id))
    if request.form.get('action') == 'delete':
        (UPLOADS / work['filename']).unlink(missing_ok=True)
    return redirect(url_for('space', token=token, edit='1'))


@app.route('/admin/login', methods=['GET', 'POST'])
def login():
    error = None
    if request.method == 'POST':
        password = os.environ.get('ADMIN_PASSWORD', '')
        if password and secrets.compare_digest(request.form.get('password', ''), password):
            session.clear()
            session['admin'] = True
            return redirect(url_for('admin'))
        error = '密码不正确，或尚未配置管理员密码。'
    return render_template('login.html', error=error)


@app.post('/admin/logout')
def logout():
    session.clear()
    return redirect(url_for('login'))


@app.route('/admin', methods=['GET', 'POST'])
@admin_required
def admin():
    if request.method == 'POST':
        class_name = request.form.get('class_name', '').strip()
        lines = [line.strip() for line in request.form.get('names', '').splitlines() if line.strip()]
        if not class_name or len(class_name) > 80 or not lines or len(lines) > 200:
            abort(400, '请填写班级和 1–200 行学号、姓名')
        roster = []
        seen = set()
        for line in lines:
            fields = re.split(r'[,，\t ]+', line, maxsplit=1)
            if len(fields) != 2 or not re.fullmatch(r'[A-Za-z0-9-]{1,32}', fields[0]) or not fields[1].strip() or len(fields[1].strip()) > 80:
                abort(400, '每行请填写“学号 姓名”，可用空格、逗号或制表符分隔')
            number, name = fields[0], fields[1].strip()
            if number in seen:
                abort(400, '导入名单中有重复学号，请核对后重试')
            seen.add(number)
            roster.append((number, name))
        with db() as conn:
            conn.execute('BEGIN IMMEDIATE')
            for number, name in roster:
                existing = conn.execute('SELECT * FROM students WHERE class_name=? AND student_number=?', (class_name, number)).fetchall()
                if existing:
                    if len(existing) != 1 or existing[0]['name'] != name:
                        abort(400, '该班级学号已对应其他学生，请核对名单')
                    continue
                legacy = conn.execute("SELECT id FROM students WHERE class_name=? AND name=? AND student_number=''", (class_name, name)).fetchall()
                if len(legacy) > 1:
                    abort(400, '旧名单中存在同班同名空间，请先由老师核对，避免二维码对应错误')
                if legacy:
                    conn.execute('UPDATE students SET student_number=? WHERE id=?', (number, legacy[0]['id']))
                else:
                    conn.execute('INSERT INTO students (class_name, name, student_number, token) VALUES (?, ?, ?, ?)', (class_name, name, number, secrets.token_urlsafe(32)))
        return redirect(url_for('admin'))
    with db() as conn:
        children = conn.execute('SELECT students.*, count(works.id) AS count FROM students LEFT JOIN works ON works.student_id=students.id GROUP BY students.id ORDER BY class_name, students.id').fetchall()
    return render_template('admin.html', children=children)


def export_component(value):
    # Keep Chinese names readable while preventing archive paths and control characters.
    cleaned = re.sub(r'[\\/<>:"|?*\x00-\x1f\x7f]', '_', value).strip(' .')
    return cleaned[:100] or '未命名'


@app.get('/admin/export')
@admin_required
def export_works():
    with db() as conn:
        rows = conn.execute('SELECT students.id AS child_id, students.class_name, students.name, students.student_number, works.* FROM students LEFT JOIN works ON works.student_id=students.id ORDER BY students.class_name, students.id, works.id').fetchall()
    archive = tempfile.TemporaryFile()
    try:
        manifest = io.StringIO(newline='')
        writer = csv.writer(manifest)
        writer.writerow(['班级', '学号', '姓名', '作品名称', '原文件名', '上传时间', '导出路径', '状态'])
        folders = {}
        used = set()
        with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_STORED) as output:
            for row in rows:
                if row['child_id'] not in folders:
                    folder = export_component(row['class_name']) + '/' + export_component((row['student_number'] or '待补学号') + '_' + row['name'])
                    if folder in used:
                        folder += '_空间' + str(row['child_id'])
                    used.add(folder)
                    folders[row['child_id']] = folder
                    output.writestr(folder + '/', '')
                if row['id'] is None:
                    writer.writerow([row['class_name'], row['student_number'], row['name'], '', '', '', '', '尚未上传'])
                    continue
                path = folders[row['child_id']] + '/' + str(row['id']) + '_' + export_component(row['original_name'])
                source = UPLOADS / row['filename']
                status = '文件缺失'
                if source.is_file():
                    output.write(source, path)
                    status = '已导出'
                writer.writerow([row['class_name'], row['student_number'], row['name'], row['title'], row['original_name'], row['created_at'], path, status])
            output.writestr('作品清单.csv', ('\ufeff' + manifest.getvalue()).encode('utf-8'))
        archive.seek(0)
        response = send_file(archive, mimetype='application/zip', as_attachment=True, download_name='研学营作品_' + datetime.now(TZ).strftime('%Y%m%d_%H%M%S') + '.zip')
        response.call_on_close(archive.close)
        return response
    except BaseException:
        archive.close()
        raise


@app.get('/admin/cards')
@admin_required
def cards():
    with db() as conn:
        children = conn.execute('SELECT * FROM students ORDER BY class_name, id').fetchall()
    base = os.environ.get('PUBLIC_BASE_URL', '').rstrip('/')
    if not base:
        abort(500, '请设置 PUBLIC_BASE_URL 为孩子们可以访问的网站地址')
    return render_template('cards.html', children=children, base=base, labels=request.args.get('layout') == 'labels')


@app.get('/admin/qr/<token>')
@admin_required
def qr(token):
    student(token)
    base = os.environ.get('PUBLIC_BASE_URL', '').rstrip('/')
    if not base:
        abort(500, '请设置 PUBLIC_BASE_URL')
    from io import BytesIO
    out = BytesIO()
    qrcode.make(base + url_for('space', token=token)).save(out, format='PNG')
    out.seek(0)
    return send_file(out, mimetype='image/png')


init_db()
