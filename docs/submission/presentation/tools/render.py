#!/usr/bin/env python3
"""PPTX를 LibreOffice로 PDF(와 검수용 슬라이드 이미지)로 바꾼다.

    python3 tools/render.py <deck.pptx> <out.pdf> [--images <폴더>] [--dpi 80]

LibreOffice는 한글과 영문·숫자 사이에 간격을 자동으로 넣는다(PowerPoint에는 없는 기능).
PowerPoint에서 보이는 모양에 맞추려고 PPTX → ODP로 바꾼 뒤 그 설정(style:text-autospace)을
끄고 PDF로 내보낸다. 글꼴은 이 PC의 fontconfig 대체 규칙을 따른다(맑은 고딕이 없으면 대체 글꼴).
"""
import argparse
import os
import shutil
import subprocess
import sys
import tempfile
import zipfile

SOFFICE = os.environ.get('SOFFICE') or shutil.which('soffice') or 'soffice'
WRAP = os.environ.get('SOFFICE_WRAPPER')  # 샌드박스에서 soffice를 감싸 실행하는 스크립트(있으면)


def soffice(args, cwd):
    cmd = ([sys.executable, WRAP] if WRAP else [SOFFICE]) + ['--headless'] + args
    subprocess.run(cmd, cwd=cwd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=600)


def patch_odp(src, dst):
    """기본 문단 속성의 한글·영문 자동 간격을 끈다. mimetype은 첫 항목, 압축 없이."""
    with zipfile.ZipFile(src) as zin, zipfile.ZipFile(dst, 'w') as zout:
        names = [i.filename for i in zin.infolist()]
        names.sort(key=lambda n: n != 'mimetype')
        hit = 0
        for n in names:
            data = zin.read(n)
            if n in ('styles.xml', 'content.xml'):
                s = data.decode('utf-8')
                hit += s.count('style:text-autospace="ideograph-alpha"')
                data = s.replace('style:text-autospace="ideograph-alpha"', 'style:text-autospace="none"').encode('utf-8')
            zout.writestr(zipfile.ZipInfo(n) if n == 'mimetype' else n, data,
                          zipfile.ZIP_STORED if n == 'mimetype' else zipfile.ZIP_DEFLATED)
    if not hit:
        print('주의: style:text-autospace를 찾지 못했습니다(자동 간격이 남을 수 있음)', file=sys.stderr)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('pptx')
    ap.add_argument('pdf')
    ap.add_argument('--images')
    ap.add_argument('--dpi', type=int, default=80)
    a = ap.parse_args()
    pptx = os.path.abspath(a.pptx)
    pdf = os.path.abspath(a.pdf)
    base = os.path.splitext(os.path.basename(pptx))[0]
    with tempfile.TemporaryDirectory(prefix='eum-render-') as tmp:
        soffice(['--convert-to', 'odp', pptx], tmp)
        odp = os.path.join(tmp, base + '.odp')
        fixed_dir = os.path.join(tmp, 'fixed')
        os.mkdir(fixed_dir)
        fixed = os.path.join(fixed_dir, base + '.odp')
        patch_odp(odp, fixed)
        soffice(['--convert-to', 'pdf', fixed], fixed_dir)
        shutil.copyfile(os.path.join(fixed_dir, base + '.pdf'), pdf)
    if a.images:
        os.makedirs(a.images, exist_ok=True)
        for f in os.listdir(a.images):
            if f.startswith('slide-') and f.endswith('.jpg'):
                os.remove(os.path.join(a.images, f))
        subprocess.run(['pdftoppm', '-jpeg', '-r', str(a.dpi), pdf, os.path.join(a.images, 'slide')], check=True)
        print(len([f for f in os.listdir(a.images) if f.startswith('slide-')]), '장')


if __name__ == '__main__':
    main()
