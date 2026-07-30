package app

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// generateTrayIcon 生成显示缓存命中率的系统托盘图标（类似电池图标样式）
func generateTrayIcon(hitRate float64) ([]byte, error) {
	// 创建 32x32 图标
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// 透明背景
	draw.Draw(img, img.Bounds(), image.Transparent, image.Point{}, draw.Src)

	percentage := hitRate * 100

	// 根据命中率选择颜色
	var fillColor, borderColor, textColor color.Color
	if percentage >= 70 {
		// 高命中率 - 绿色
		fillColor = color.RGBA{0x6e, 0xe7, 0xa5, 0xff}    // #6ee7a5
		borderColor = color.RGBA{0x6e, 0xe7, 0xa5, 0xff}
		textColor = color.RGBA{0x0c, 0x18, 0x14, 0xff}
	} else if percentage >= 40 {
		// 中命中率 - 黄色
		fillColor = color.RGBA{0xf3, 0x9c, 0x12, 0xff}
		borderColor = color.RGBA{0xf3, 0x9c, 0x12, 0xff}
		textColor = color.RGBA{0x18, 0x10, 0x08, 0xff}
	} else {
		// 低命中率 - 红色
		fillColor = color.RGBA{0xe7, 0x4c, 0x3c, 0xff}
		borderColor = color.RGBA{0xe7, 0x4c, 0x3c, 0xff}
		textColor = color.RGBA{0xff, 0xff, 0xff, 0xff}
	}

	// 绘制矩形边框（2像素粗）
	const padding = 2
	const borderWidth = 2
	rect := image.Rect(padding, padding, size-padding, size-padding)
	
	// 填充背景色
	draw.Draw(img, rect, &image.Uniform{fillColor}, image.Point{}, draw.Src)

	// 绘制边框
	for i := 0; i < borderWidth; i++ {
		drawRect(img, image.Rect(padding+i, padding+i, size-padding-i, size-padding-i), borderColor)
	}

	// 绘制百分比文字
	text := fmt.Sprintf("%.0f%%", percentage)
	
	drawer := &font.Drawer{
		Dst:  img,
		Src:  &image.Uniform{textColor},
		Face: basicfont.Face7x13,
	}

	// 计算文字位置（居中）
	textWidth := drawer.MeasureString(text)
	x := (fixed.I(size) - textWidth) / 2
	y := fixed.I(size/2 + 4)

	drawer.Dot = fixed.Point26_6{X: x, Y: y}
	drawer.DrawString(text)

	// 编码为 PNG
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// drawRect 绘制矩形边框
func drawRect(img *image.RGBA, rect image.Rectangle, c color.Color) {
	// 上边
	for x := rect.Min.X; x < rect.Max.X; x++ {
		img.Set(x, rect.Min.Y, c)
	}
	// 下边
	for x := rect.Min.X; x < rect.Max.X; x++ {
		img.Set(x, rect.Max.Y-1, c)
	}
	// 左边
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		img.Set(rect.Min.X, y, c)
	}
	// 右边
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		img.Set(rect.Max.X-1, y, c)
	}
}