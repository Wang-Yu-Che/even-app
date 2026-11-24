//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreText -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <CoreText/CoreText.h>
#include <stdlib.h>

static void drawLyric(CGContextRef ctx, const char *text, double width, double y, int size, int level) {
	CFStringRef string = CFStringCreateWithCString(NULL, text, kCFStringEncodingUTF8);
	if (!string) return;
	CTFontRef font = CTFontCreateUIFontForLanguage(kCTFontUIFontSystem, size, NULL);
	const void *keys[] = {kCTFontAttributeName, kCTForegroundColorFromContextAttributeName};
	const void *values[] = {font, kCFBooleanTrue};
	CFDictionaryRef attrs = CFDictionaryCreate(NULL, keys, values, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFAttributedStringRef attributed = CFAttributedStringCreate(NULL, string, attrs);
	CTLineRef line = CTLineCreateWithAttributedString(attributed);
	double ascent, descent;
	double advance = CTLineGetTypographicBounds(line, &ascent, &descent, NULL);
	double scale = advance > width ? width / advance : 1;
	CGContextSaveGState(ctx);
	CGContextSetGrayFillColor(ctx, level / 15.0, 1);
	CGContextSetTextMatrix(ctx, CGAffineTransformIdentity);
	CGContextTranslateCTM(ctx, (width - advance * scale) / 2, y - (ascent - descent) / 2);
	CGContextScaleCTM(ctx, scale, 1);
	CGContextSetTextPosition(ctx, 0, 0);
	CTLineDraw(line, ctx);
	CGContextRestoreGState(ctx);
	CFRelease(line);
	CFRelease(attributed);
	CFRelease(attrs);
	CFRelease(font);
	CFRelease(string);
}

static void drawFrame(unsigned char *pixels, int width, int height, const char *previous, const char *current, const char *next) {
	CGColorSpaceRef gray = CGColorSpaceCreateDeviceGray();
	CGContextRef ctx = CGBitmapContextCreate(pixels, width, height, 8, width, gray, kCGImageAlphaNone);
	CGColorSpaceRelease(gray);
	if (!ctx) return;
	if (*previous) drawLyric(ctx, previous, width, 50, 13, 8);
	if (*current) drawLyric(ctx, current, width, 29, 22, 15);
	if (*next) drawLyric(ctx, next, width, 8, 13, 8);
	CGContextRelease(ctx);
}
*/
import "C"

import (
	"strings"
	"unsafe"

	"github.com/Wang-Yu-Che/even-g2-go/g2"
)

// Plain-text lyrics for now; flip back to true to render them as a bitmap.
const bitmapLyrics = false

func mediaLyricsIcon(rows [3]string) g2.StatusIcon {
	const width, height = 232, 58
	var pixels [width * height]byte
	var text [3]*C.char
	for i, row := range rows {
		text[i] = C.CString(strings.Join(strings.Fields(row), " "))
		defer C.free(unsafe.Pointer(text[i]))
	}
	C.drawFrame((*C.uchar)(&pixels[0]), width, height, text[0], text[1], text[2])
	return mediaBitmap(4, "media-lyrics", 320, 58, width, height, func(x, y int) uint8 {
		return pixels[y*width+x] / 17
	})
}
