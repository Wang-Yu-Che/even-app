//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Vision -framework Foundation -framework CoreVideo
#import <Vision/Vision.h>
#include <stdlib.h>

// subjectMask writes a 0..1 foreground mask (Vision's subject lifting) at
// width×height into out. Returns 0 when no subject is found or on error.
static int subjectMask(const void *data, int length, int width, int height, float *out) {
	@autoreleasepool {
		if (@available(macOS 14.0, *)) {
			NSData *bytes = [NSData dataWithBytes:data length:length];
			VNImageRequestHandler *handler = [[VNImageRequestHandler alloc] initWithData:bytes options:@{}];
			VNGenerateForegroundInstanceMaskRequest *request = [[VNGenerateForegroundInstanceMaskRequest alloc] init];
			if (![handler performRequests:@[request] error:nil]) return 0;
			VNInstanceMaskObservation *result = request.results.firstObject;
			if (!result || result.allInstances.count == 0) return 0;
			CVPixelBufferRef mask = [result generateScaledMaskForImageForInstances:result.allInstances
				fromRequestHandler:handler error:nil];
			if (!mask) return 0;
			CVPixelBufferLockBaseAddress(mask, kCVPixelBufferLock_ReadOnly);
			int mw = (int)CVPixelBufferGetWidth(mask), mh = (int)CVPixelBufferGetHeight(mask);
			size_t stride = CVPixelBufferGetBytesPerRow(mask);
			const unsigned char *base = CVPixelBufferGetBaseAddress(mask);
			for (int y = 0; y < height; y++) {
				const float *row = (const float *)(base + (size_t)(y * mh / height) * stride);
				for (int x = 0; x < width; x++) out[y * width + x] = row[x * mw / width];
			}
			CVPixelBufferUnlockBaseAddress(mask, kCVPixelBufferLock_ReadOnly);
			return 1;
		}
		return 0;
	}
}
*/
import "C"

import "unsafe"

// coverSubjectMask returns a width×height 0..1 mask of the cover's main
// subject, or nil if Vision finds none (or the OS is older than macOS 14).
func coverSubjectMask(data []byte, width, height int) []float32 {
	if len(data) == 0 {
		return nil
	}
	mask := make([]float32, width*height)
	if C.subjectMask(unsafe.Pointer(&data[0]), C.int(len(data)), C.int(width), C.int(height), (*C.float)(&mask[0])) == 0 {
		return nil
	}
	return mask
}
