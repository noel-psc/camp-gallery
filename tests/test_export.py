import io
import os
import sqlite3
import tempfile
import unittest
import zipfile
from pathlib import Path

TEMP = tempfile.TemporaryDirectory()
os.environ['DATA_DIR'] = TEMP.name
os.environ['SECRET_KEY'] = 'test-secret'
os.environ['ADMIN_PASSWORD'] = 'test-password'
os.environ['PUBLIC_BASE_URL'] = 'https://camp.example.test'

# Exercise upgrade from the original database schema.
with sqlite3.connect(Path(TEMP.name) / 'camp.sqlite3') as conn:
    conn.execute('CREATE TABLE students (id INTEGER PRIMARY KEY, class_name TEXT NOT NULL, name TEXT NOT NULL, token TEXT UNIQUE NOT NULL)')
    conn.execute("INSERT INTO students VALUES (1, '四年级一班', '小明', 'first')")

import app


class ExportTests(unittest.TestCase):
    def setUp(self):
        self.client = app.app.test_client()
        app.DEADLINE = '2099-11-19T23:59:59+08:00'
        with app.db() as conn:
            conn.execute('DELETE FROM works')
            conn.execute('DELETE FROM students')
            conn.executemany('INSERT INTO students (id, class_name, name, token) VALUES (?, ?, ?, ?)', [(1, '四年级一班', '小明', 'first'), (2, '四年级一班', '小红', 'second'), (3, '四年级二班', '小亮', 'third')])

    def upload(self, token='first', **fields):
        return self.client.post('/s/' + token, data={**fields, 'files': (io.BytesIO(b'photo-content'), '照片.png')}, content_type='multipart/form-data')

    def test_identity_required_and_bound(self):
        self.assertEqual(self.upload().status_code, 400)
        self.assertEqual(self.upload(name='其他人', student_number='001').status_code, 400)
        self.assertEqual(self.upload(name='小明', student_number='001').status_code, 302)
        self.assertEqual(self.upload(name='小明', student_number='002').status_code, 400)
        self.assertEqual(self.upload('second', name='小红', student_number='001').status_code, 400)
        self.assertEqual(self.upload('third', name='小亮', student_number='001').status_code, 302)
        with app.db() as conn:
            self.assertEqual(conn.execute('SELECT student_number FROM students WHERE id=1').fetchone()[0], '001')
            self.assertEqual(conn.execute('SELECT count(*) FROM works').fetchone()[0], 2)

    def test_export_original_bytes_and_read_only_deadline(self):
        self.assertEqual(self.client.get('/admin/export').status_code, 302)
        self.upload(name='小明', student_number='001')
        self.upload(name='小明', student_number='001')
        app.DEADLINE = '2000-01-01T00:00:00+08:00'
        self.assertEqual(self.upload(name='小明', student_number='001').status_code, 403)
        self.client.post('/admin/login', data={'password': 'test-password'})
        self.assertIn('导出全部作品'.encode(), self.client.get('/admin').data)
        response = self.client.get('/admin/export')
        self.assertEqual(response.status_code, 200)
        with zipfile.ZipFile(io.BytesIO(response.data)) as archive:
            photos = [n for n in archive.namelist() if n.endswith('.png')]
            self.assertEqual(len(photos), 2)
            for name in photos:
                self.assertTrue(name.startswith('四年级一班/001_小明/'))
                self.assertEqual(archive.read(name), b'photo-content')
            self.assertIn('四年级二班/待补学号_小亮/', archive.namelist())
            self.assertIn('尚未上传', archive.read('作品清单.csv').decode('utf-8-sig'))
        response.close()

    def login(self):
        self.client.post('/admin/login', data={'password': 'test-password'})

    def test_roster_import_binds_identity_and_preserves_qr(self):
        self.login()
        response = self.client.post('/admin', data={'class_name': '四年级一班', 'names': '001 小明\n002,小红\n003\t小新'})
        self.assertEqual(response.status_code, 302)
        with app.db() as conn:
            students = conn.execute("SELECT * FROM students WHERE class_name='四年级一班' ORDER BY student_number").fetchall()
        self.assertEqual([(s['student_number'], s['name']) for s in students], [('001', '小明'), ('002', '小红'), ('003', '小新')])
        self.assertEqual(students[0]['token'], 'first')
        original_tokens = [s['token'] for s in students]
        self.assertEqual(self.client.post('/admin', data={'class_name': '四年级一班', 'names': '001 小明\n002 小红\n003 小新'}).status_code, 302)
        with app.db() as conn:
            self.assertEqual([r[0] for r in conn.execute("SELECT token FROM students WHERE class_name='四年级一班' ORDER BY student_number")], original_tokens)
        page = self.client.get('/s/first').get_data(as_text=True)
        self.assertIn('学号 001', page)
        self.assertNotIn('name="student_number"', page)
        self.assertNotIn('name="name"', page)
        self.assertEqual(self.upload().status_code, 302)
        self.assertEqual(self.upload(student_number='999').status_code, 400)

    def test_roster_conflicts_roll_back_whole_batch(self):
        self.login()
        with app.db() as conn:
            conn.execute("UPDATE students SET student_number='001' WHERE id=1")
        for names in ['004 新同学\n001 其他人', '004 新同学\n004 重复学号', '只有姓名', '学号 姓名']:
            self.assertEqual(self.client.post('/admin', data={'class_name': '四年级一班', 'names': names}).status_code, 400)
        with app.db() as conn:
            self.assertEqual(conn.execute('SELECT count(*) FROM students').fetchone()[0], 3)
        self.assertEqual(self.client.post('/admin', data={'class_name': '四年级二班', 'names': '001 小亮'}).status_code, 302)

    def test_ambiguous_legacy_roster_rejected(self):
        self.login()
        with app.db() as conn:
            conn.execute("INSERT INTO students (class_name, name, token) VALUES ('四年级一班', '小明', 'ambiguous')")
        self.assertEqual(self.client.post('/admin', data={'class_name': '四年级一班', 'names': '001 小明'}).status_code, 400)
        with app.db() as conn:
            self.assertEqual(conn.execute("SELECT count(*) FROM students WHERE student_number!=''").fetchone()[0], 0)

    def test_scan_upload_view_and_edit_flow(self):
        page = self.client.get('/s/first').get_data(as_text=True)
        self.assertIn('上传你的第一件作品', page)
        response = self.upload(student_number='001', title='我的画')
        self.assertEqual(response.location, '/s/first')
        page = self.client.get(response.location).get_data(as_text=True)
        self.assertIn('我的画', page)
        self.assertIn('编辑作品', page)
        self.assertNotIn('type="file"', page)
        self.assertNotIn('保存名称', page)
        page = self.client.get('/s/first?edit=1').get_data(as_text=True)
        self.assertIn('type="file"', page)
        self.assertIn('保存名称', page)
        self.assertIn('完成编辑，查看作品', page)
        with app.db() as conn:
            work_id = conn.execute('SELECT id FROM works').fetchone()[0]
        self.assertEqual(self.client.post(f'/s/second/work/{work_id}', data={'title': '越界'}).status_code, 404)
        response = self.client.post(f'/s/first/work/{work_id}', data={'title': '新名称', 'action': 'save'})
        self.assertIn('edit=1', response.location)
        self.assertIn('新名称', self.client.get('/s/first').get_data(as_text=True))
        media = self.client.get(f'/s/first/file/{work_id}')
        self.assertEqual(media.data, b'photo-content')
        media.close()
        app.DEADLINE = '2000-01-01T00:00:00+08:00'
        page = self.client.get('/s/first?edit=1').get_data(as_text=True)
        self.assertIn('新名称', page)
        self.assertNotIn('编辑作品', page)
        self.assertNotIn('type="file"', page)
        self.assertEqual(self.client.post(f'/s/first/work/{work_id}', data={'action': 'delete'}).status_code, 403)
        app.DEADLINE = '2099-11-19T23:59:59+08:00'
        self.client.post(f'/s/first/work/{work_id}', data={'action': 'delete'})
        self.assertIn('上传你的第一件作品', self.client.get('/s/first').get_data(as_text=True))

    def test_upload_progress_only_on_upload_pages(self):
        page = self.client.get('/s/first').get_data(as_text=True)
        self.assertIn('id="upload-form"', page)
        self.assertIn('id="upload-progress"', page)
        self.assertIn('data-max-upload-mb="200"', page)
        self.assertIn('/static/upload.js', page)
        self.assertIn('enctype="multipart/form-data"', page)
        self.upload(student_number='001')
        page = self.client.get('/s/first').get_data(as_text=True)
        self.assertNotIn('/static/upload.js', page)
        self.assertNotIn('id="upload-form"', page)
        page = self.client.get('/s/first?edit=1').get_data(as_text=True)
        self.assertIn('id="upload-progress"', page)
        app.DEADLINE = '2000-01-01T00:00:00+08:00'
        self.assertNotIn('/static/upload.js', self.client.get('/s/first?edit=1').get_data(as_text=True))

    def test_cards_and_labels_share_student_qr(self):
        self.assertEqual(self.client.get('/admin/cards?layout=labels').status_code, 302)
        self.assertEqual(self.client.get('/admin/qr/first').status_code, 302)
        self.login()
        self.client.post('/admin', data={'class_name': '四年级一班', 'names': '001 小明'})
        for path in ['/admin/cards', '/admin/cards?layout=labels']:
            page = self.client.get(path).get_data(as_text=True)
            self.assertIn('/admin/qr/first', page)
            self.assertIn('学号：001', page)
            self.assertIn('首次扫码上传', page)
        response = self.client.get('/admin/qr/first')
        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.mimetype, 'image/png')
        expected = io.BytesIO()
        app.qrcode.make('https://camp.example.test/s/first').save(expected, format='PNG')
        self.assertEqual(response.data, expected.getvalue())
        response.close()

    def test_safe_archive_paths(self):
        with app.db() as conn:
            conn.execute("UPDATE students SET class_name='../一班', name='../../名字' WHERE id=1")
        self.upload(name='../../名字', student_number='001')
        self.client.post('/admin/login', data={'password': 'test-password'})
        response = self.client.get('/admin/export')
        with zipfile.ZipFile(io.BytesIO(response.data)) as archive:
            for name in archive.namelist():
                self.assertNotIn('..', name.split('/'))
                self.assertFalse(name.startswith('/'))
        response.close()


if __name__ == '__main__':
    unittest.main()
