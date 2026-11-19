# -*- coding: utf-8 -*-
"""用 qBittorrent 官方 logo 生成 UPK / PWA 三套图标。

源图：assets/qbittorrent_logo.png
  qBittorrent 4.0+ 官方 logo（仓库 src/icons/qbittorrent-tray.svg 的 1280px 渲染版，
  Wikimedia Commons File:New_qBittorrent_Logo.svg，GPLv3）。
  透明底、蓝色渐变圆、白色 qb，与 mihomo 那个用官方猫 logo 的做法一致：
  不自绘、不套深色方板，直接让品牌图形当图标。

产出：
  pkg/rootfs_common/icon.png   256x256  UGOS 应用列表图标
  app/web/icon.png             512x512  页面 favicon / 侧栏品牌 / PWA
  app/web/icon-maskable.png    512x512  Android maskable：满幅渐变底 + 安全区内的 logo

用法：python assets/make_icons.py
"""
import os

from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(ROOT, 'assets', 'qbittorrent_logo.png')

# 官方 SVG 里的渐变端点：linearGradient #72b4f5 -> #356ebf
TOP = (114, 180, 245)
BOTTOM = (53, 110, 191)


def load_logo():
    im = Image.open(SRC).convert('RGBA')
    # 源图四角全透明，但圆周几乎贴边；裁掉纯透明边得到正方形，避免缩放时留出不等宽白边
    box = im.getchannel('A').getbbox()
    im = im.crop(box)
    side = max(im.size)
    sq = Image.new('RGBA', (side, side), (0, 0, 0, 0))
    sq.alpha_composite(im, ((side - im.width) // 2, (side - im.height) // 2))
    return sq


def gradient(size):
    """竖向线性渐变，给 maskable 图标当满幅底，颜色取自官方 logo。"""
    im = Image.new('RGBA', (size, size))
    px = im.load()
    for y in range(size):
        t = y / (size - 1)
        row = tuple(int(TOP[i] + (BOTTOM[i] - TOP[i]) * t) for i in range(3)) + (255,)
        for x in range(size):
            px[x, y] = row
    return im


def make(logo, size, frac, bg=None):
    """logo 按 frac 占画布边长居中合成；bg 为空则保持透明底。"""
    canvas = bg.copy() if bg is not None else Image.new('RGBA', (size, size), (0, 0, 0, 0))
    d = int(size * frac)
    small = logo.resize((d, d), Image.LANCZOS)
    canvas.alpha_composite(small, ((size - d) // 2, (size - d) // 2))
    return canvas


def save(im, rel):
    path = os.path.join(ROOT, rel)
    im.save(path, format='PNG', optimize=True)
    print('%-42s %4dx%-4d %7d B' % (rel, im.width, im.height, os.path.getsize(path)))


def main():
    logo = load_logo()
    # UGOS 图标透明底，logo 留一圈呼吸空间（与 mihomo 的 256px 图标同规格）
    save(make(logo, 256, 0.88), 'pkg/rootfs_common/icon.png')
    # 页面与 PWA 用 512，同一份图，侧栏缩小到 36 CSS px 时 qb 仍可辨
    save(make(logo, 512, 0.88), 'app/web/icon.png')
    # maskable 的安全区是中心 80% 直径的圆；logo 只占 62% 直径，裁切时不会被切到
    save(make(logo, 512, 0.62, gradient(512)), 'app/web/icon-maskable.png')


if __name__ == '__main__':
    main()
