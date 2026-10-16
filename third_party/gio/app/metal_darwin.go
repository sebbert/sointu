// SPDX-License-Identifier: Unlicense OR MIT

//go:build !nometal
// +build !nometal

package app

import (
	"errors"
	"sync/atomic"

	"gioui.org/gpu"
)

/*
#cgo CFLAGS: -Werror -xobjective-c -fobjc-arc
#cgo LDFLAGS: -framework QuartzCore -framework Metal

#import <Metal/Metal.h>
#import <QuartzCore/CAMetalLayer.h>

#include <CoreFoundation/CoreFoundation.h>

static CFTypeRef createMetalDevice(void) {
	@autoreleasepool {
		id<MTLDevice> dev = MTLCreateSystemDefaultDevice();
		return CFBridgingRetain(dev);
	}
}

static void setupLayer(CFTypeRef layerRef, CFTypeRef devRef) {
	@autoreleasepool {
		CAMetalLayer *layer = (__bridge CAMetalLayer *)layerRef;
		id<MTLDevice> dev = (__bridge id<MTLDevice>)devRef;
		layer.device = dev;
		// Package gpu assumes an sRGB-encoded framebuffer.
		layer.pixelFormat = MTLPixelFormatBGRA8Unorm_sRGB;
		if (@available(iOS 11.0, *)) {
			// Never let nextDrawable time out and return nil.
			layer.allowsNextDrawableTimeout = NO;
		}
	}
}

static CFTypeRef nextDrawable(CFTypeRef layerRef) {
	@autoreleasepool {
		CAMetalLayer *layer = (__bridge CAMetalLayer *)layerRef;
		return CFBridgingRetain([layer nextDrawable]);
	}
}

// drawableFits tells if a drawable has the size that the layer has now.
static int drawableFits(CFTypeRef layerRef, CFTypeRef drawableRef) {
	@autoreleasepool {
		CAMetalLayer *layer = (__bridge CAMetalLayer *)layerRef;
		id<CAMetalDrawable> drawable = (__bridge id<CAMetalDrawable>)drawableRef;
		CGSize size = layer.drawableSize;
		return drawable.texture.width == (NSUInteger)size.width && drawable.texture.height == (NSUInteger)size.height;
	}
}

static CFTypeRef drawableTexture(CFTypeRef drawableRef) {
	@autoreleasepool {
		id<CAMetalDrawable> drawable = (__bridge id<CAMetalDrawable>)drawableRef;
		return CFBridgingRetain(drawable.texture);
	}
}

static void presentDrawable(CFTypeRef queueRef, CFTypeRef drawableRef) {
	@autoreleasepool {
		id<MTLDrawable> drawable = (__bridge id<MTLDrawable>)drawableRef;
		id<MTLCommandQueue> queue = (__bridge id<MTLCommandQueue>)queueRef;
		id<MTLCommandBuffer> cmdBuffer = [queue commandBuffer];
		[cmdBuffer commit];
		[cmdBuffer waitUntilScheduled];
		[drawable present];
	}
}

static CFTypeRef newCommandQueue(CFTypeRef devRef) {
	@autoreleasepool {
		id<MTLDevice> dev = (__bridge id<MTLDevice>)devRef;
		return CFBridgingRetain([dev newCommandQueue]);
	}
}
*/
import "C"

type mtlContext struct {
	dev      C.CFTypeRef
	view     C.CFTypeRef
	layer    C.CFTypeRef
	queue    C.CFTypeRef
	drawable C.CFTypeRef
	texture  C.CFTypeRef
	// With PresentAsync the drawable of the next frame is got ahead of
	// time, off the main thread: next is where it comes, fetching is set
	// while it is on its way, and waiting while a frame was left out for
	// it, which wake asks for again.
	next     chan C.CFTypeRef
	fetching bool
	waiting  *atomic.Bool
	wake     func()
}

func newMtlContext(w *window) (*mtlContext, error) {
	dev := C.createMetalDevice()
	if dev == 0 {
		return nil, errors.New("metal: MTLCreateSystemDefaultDevice failed")
	}
	view := w.contextView()
	layer := getMetalLayer(view)
	if layer == 0 {
		C.CFRelease(dev)
		return nil, errors.New("metal: CAMetalLayer construction failed")
	}
	queue := C.newCommandQueue(dev)
	if queue == 0 {
		C.CFRelease(dev)
		C.CFRelease(layer)
		return nil, errors.New("metal: [MTLDevice newCommandQueue] failed")
	}
	C.setupLayer(layer, dev)
	c := &mtlContext{
		dev:     dev,
		view:    view,
		layer:   layer,
		queue:   queue,
		next:    make(chan C.CFTypeRef, 1),
		waiting: new(atomic.Bool),
		wake:    w.Invalidate,
	}
	return c, nil
}

func (c *mtlContext) RenderTarget() (gpu.RenderTarget, error) {
	if c.drawable != 0 || c.texture != 0 {
		return nil, errors.New("metal:a previous RenderTarget wasn't Presented")
	}
	if PresentAsync {
		d, ok := c.fetched()
		if !ok {
			return nil, errNoFrame
		}
		c.drawable = d
	} else {
		c.drawable = C.nextDrawable(c.layer)
	}
	if c.drawable == 0 {
		return nil, errors.New("metal: [CAMetalLayer nextDrawable] failed")
	}
	c.texture = C.drawableTexture(c.drawable)
	if c.texture == 0 {
		return nil, errors.New("metal: CADrawable.texture is nil")
	}
	return gpu.MetalRenderTarget{
		Texture: uintptr(c.texture),
	}, nil
}

// fetch gets the next drawable off the main thread, where getting it can
// wait.
func (c *mtlContext) fetch() {
	if c.fetching {
		return
	}
	c.fetching = true
	layer, next, waiting, wake := c.layer, c.next, c.waiting, c.wake
	C.CFRetain(layer)
	go func() {
		d := C.nextDrawable(layer)
		C.CFRelease(layer)
		next <- d
		if d != 0 && waiting.Swap(false) {
			wake()
		}
	}()
}

// fetched returns the drawable that was got ahead of time, if it has come.
// One of another size than the layer has now, as while the window is
// resized, is of no use: the right one is waited for, as without
// PresentAsync.
func (c *mtlContext) fetched() (C.CFTypeRef, bool) {
	c.waiting.Store(true)
	select {
	case d := <-c.next:
		c.waiting.Store(false)
		c.fetching = false
		switch {
		case d == 0:
			return 0, false
		case C.drawableFits(c.layer, d) == 0:
			C.CFRelease(d)
			return C.nextDrawable(c.layer), true
		}
		return d, true
	default:
		c.fetch()
		return 0, false
	}
}

func (c *mtlContext) API() gpu.API {
	return gpu.Metal{
		Device:      uintptr(c.dev),
		Queue:       uintptr(c.queue),
		PixelFormat: int(C.MTLPixelFormatBGRA8Unorm_sRGB),
	}
}

func (c *mtlContext) Release() {
	if next := c.next; c.fetching {
		// the drawable that is on its way
		go func() {
			if d := <-next; d != 0 {
				C.CFRelease(d)
			}
		}()
	}
	C.CFRelease(c.queue)
	C.CFRelease(c.dev)
	C.CFRelease(c.layer)
	if c.drawable != 0 {
		C.CFRelease(c.drawable)
	}
	if c.texture != 0 {
		C.CFRelease(c.texture)
	}
	*c = mtlContext{}
}

func (c *mtlContext) Present() error {
	C.CFRelease(c.texture)
	c.texture = 0
	C.presentDrawable(c.queue, c.drawable)
	C.CFRelease(c.drawable)
	c.drawable = 0
	if PresentAsync {
		c.fetch()
	}
	return nil
}

func (c *mtlContext) Lock() error {
	return nil
}

func (c *mtlContext) Unlock() {}

func (c *mtlContext) Refresh() error {
	resizeDrawable(c.view, c.layer)
	return nil
}

func (w *window) NewContext() (context, error) {
	return newMtlContext(w)
}
