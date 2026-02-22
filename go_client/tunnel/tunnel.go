package tunnel

import (
	"io"
	"net/netip"
	"sync"
	"time"

	"go_client/georouting"
	"go_client/tunnel/internal"
)

type ReaderFunc func(p []byte) (int, error)
type WriterFunc func(b []byte) (int, error)

type detourIO struct {
	readFn  ReaderFunc
	writeFn WriterFunc
}

type flowKey struct {
	proto   georouting.Network
	srcIP   netip.Addr
	dstIP   netip.Addr
	srcPort uint16
	dstPort uint16
}

type flowEntry struct {
	out       georouting.Outbound
	expiresAt time.Time
}

type tunTransfer struct {
	tun     io.ReadWriteCloser
	readFn  ReaderFunc
	writeFn WriterFunc

	stopCh chan struct{}
	wg     sync.WaitGroup

	// geo
	router  georouting.Router
	sniffer DomainSniffer
	detours map[georouting.Outbound]detourIO

	// flow cache
	cacheEnabled bool
	cache        map[flowKey]flowEntry
	cacheMu      sync.Mutex // writeLoop/readLoop раздельные горутины, но cache только в readLoop; оставил на случай будущих расширений
	lastGC       time.Time
	cacheOpts    FlowCacheOptions

	// packets from backends -> tun
	toTun chan []byte
}

var (
	defaultBufSize = 65536

	transferMu   sync.Mutex
	transferInst *tunTransfer

	packetPool = sync.Pool{
		New: func() any { return make([]byte, defaultBufSize) },
	}
)

func StartTransfer(
	tun io.ReadWriteCloser,
	readFn ReaderFunc,
	writeFn WriterFunc,
) {
	StartTransferWithOptions(tun, readFn, writeFn, TransferOptions{
		Router: georouting.NewDefaultRouter(),
	})
}

func StartTransferWithOptions(
	tun io.ReadWriteCloser,
	readFn ReaderFunc,
	writeFn WriterFunc,
	opts TransferOptions,
) {
	transferMu.Lock()
	defer transferMu.Unlock()

	if transferInst != nil {
		stopLocked()
	}

	cacheOpts := opts.FlowCache
	if cacheOpts == (FlowCacheOptions{}) {
		cacheOpts = defaultFlowCacheOptions()
	}

	t := &tunTransfer{
		tun:     tun,
		readFn:  readFn,
		writeFn: writeFn,
		stopCh:  make(chan struct{}),

		router:  opts.Router,
		sniffer: opts.DomainSniffer,

		detours: make(map[georouting.Outbound]detourIO),

		cache:     make(map[flowKey]flowEntry),
		cacheOpts: cacheOpts,
		toTun:     make(chan []byte, 256),
	}

	// detours
	for _, d := range opts.Detours {
		if d.Tag == "" {
			continue
		}
		t.detours[d.Tag] = detourIO{readFn: d.ReadFn, writeFn: d.WriteFn}
	}

	// flow cache включаем только если есть router
	t.cacheEnabled = (t.router != nil) && t.cacheOpts.Enabled

	transferInst = t
	transferInst.startLoops()
}

func (t *tunTransfer) startLoops() {
	t.wg.Add(2)
	go t.readLoop()
	go t.writeLoop()
}

func (t *tunTransfer) readLoop() {
	defer t.wg.Done()

	raw := make([]byte, defaultBufSize)

	for {
		select {
		case <-t.stopCh:
			return
		default:
			n, err := t.tun.Read(raw)
			if err != nil || n <= 0 {
				continue
			}

			packet, ok := internal.AdaptReadPackets(raw[:n])
			if !ok {
				continue
			}

			// Если роутера нет — поведение как раньше.
			if t.router == nil {
				if t.writeFn != nil {
					_, _ = t.writeFn(packet)
				}
				continue
			}

			meta, flags, err := parsePacketMetadata(packet)
			if err != nil {
				// fallback: proxy
				if t.writeFn != nil {
					_, _ = t.writeFn(packet)
				}
				continue
			}

			out := t.decideWithCache(meta, flags, packet)

			switch out {
			case georouting.OutboundBlock:
				// drop
				continue

			case georouting.OutboundDirect:
				if d, ok := t.detours[georouting.OutboundDirect]; ok && d.writeFn != nil {
					_, _ = d.writeFn(packet)
					continue
				}
				// если direct detour не зарегистрирован — fallback на proxy
				if t.writeFn != nil {
					_, _ = t.writeFn(packet)
				}
				continue

			case georouting.OutboundProxy:
				fallthrough
			default:
				if t.writeFn != nil {
					_, _ = t.writeFn(packet)
				}
				continue
			}
		}
	}
}

func (t *tunTransfer) writeLoop() {
	defer t.wg.Done()

	// Поднимаем pump(ы) чтения из backend’ов (proxy + detours) в общий канал toTun.
	if t.readFn != nil {
		t.startBackendPump(t.readFn)
	}
	for tag, d := range t.detours {
		_ = tag
		if d.readFn != nil {
			t.startBackendPump(d.readFn)
		}
	}

	for {
		select {
		case <-t.stopCh:
			return
		case pkt := <-t.toTun:
			encoded, ok := internal.AdaptWritePackets(pkt)
			if ok {
				_, _ = t.tun.Write(encoded)
			}
			putPacket(pkt)
		}
	}
}

func (t *tunTransfer) startBackendPump(readFn ReaderFunc) {
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()

		buf := make([]byte, defaultBufSize)

		for {
			select {
			case <-t.stopCh:
				return
			default:
				n, err := readFn(buf)
				if err != nil || n <= 0 {
					continue
				}
				pkt := getPacket(n)
				copy(pkt, buf[:n])

				select {
				case t.toTun <- pkt:
				case <-t.stopCh:
					putPacket(pkt)
					return
				}
			}
		}
	}()
}

func (t *tunTransfer) decideWithCache(meta georouting.Metadata, flags tcpFlags, packet []byte) georouting.Outbound {
	if !t.cacheEnabled {
		// домен (если умеем) — как “ускоритель” для правил типа domain_suffix
		if t.sniffer != nil && meta.Domain == "" {
			if d, ok := t.sniffer.Sniff(packet, meta); ok {
				meta.Domain = d
			}
		}
		return t.router.Decide(meta)
	}

	// Кэшируем только TCP/UDP с валидными портами.
	if (meta.Network != georouting.NetworkTCP && meta.Network != georouting.NetworkUDP) || meta.DstPort == 0 || meta.SrcPort == 0 {
		if t.sniffer != nil && meta.Domain == "" {
			if d, ok := t.sniffer.Sniff(packet, meta); ok {
				meta.Domain = d
			}
		}
		return t.router.Decide(meta)
	}

	key := flowKey{
		proto:   meta.Network,
		srcIP:   meta.SrcIP,
		dstIP:   meta.DstIP,
		srcPort: meta.SrcPort,
		dstPort: meta.DstPort,
	}

	now := time.Now()

	// GC по интервалу
	if t.lastGC.IsZero() || now.Sub(t.lastGC) >= t.cacheOpts.GCInterval {
		t.gcFlows(now)
		t.lastGC = now
	}

	t.cacheMu.Lock()
	entry, ok := t.cache[key]
	t.cacheMu.Unlock()

	if ok && entry.expiresAt.After(now) {
		// для TCP: при FIN/RST удаляем, чтобы быстро освобождать таблицу
		if meta.Network == georouting.NetworkTCP && (flags.fin || flags.rst) {
			t.cacheMu.Lock()
			delete(t.cache, key)
			t.cacheMu.Unlock()
		} else {
			// продляем TTL “на активность”
			ttl := t.ttlFor(meta.Network)
			t.cacheMu.Lock()
			e := t.cache[key]
			e.expiresAt = now.Add(ttl)
			t.cache[key] = e
			t.cacheMu.Unlock()
		}
		return entry.out
	}

	// Cache miss: можем попробовать “доснифать” домен.
	if t.sniffer != nil && meta.Domain == "" {
		if d, ok := t.sniffer.Sniff(packet, meta); ok {
			meta.Domain = d
		}
	}

	out := t.router.Decide(meta)

	// Ограничение размера таблицы, иначе при DDoS/шуме можно раздуть память.
	t.cacheMu.Lock()
	if t.cacheOpts.MaxEntries > 0 && len(t.cache) >= t.cacheOpts.MaxEntries {
		// Простая стратегия: при переполнении — делаем GC “сейчас”
		t.cacheMu.Unlock()
		t.gcFlows(now)
		t.cacheMu.Lock()
	}
	t.cache[key] = flowEntry{out: out, expiresAt: now.Add(t.ttlFor(meta.Network))}
	t.cacheMu.Unlock()

	return out
}

func (t *tunTransfer) ttlFor(n georouting.Network) time.Duration {
	switch n {
	case georouting.NetworkTCP:
		if t.cacheOpts.TCPIdleTimeout > 0 {
			return t.cacheOpts.TCPIdleTimeout
		}
		return 2 * time.Minute
	case georouting.NetworkUDP:
		if t.cacheOpts.UDPIdleTimeout > 0 {
			return t.cacheOpts.UDPIdleTimeout
		}
		return 30 * time.Second
	default:
		return 30 * time.Second
	}
}

func (t *tunTransfer) gcFlows(now time.Time) {
	t.cacheMu.Lock()
	defer t.cacheMu.Unlock()

	for k, v := range t.cache {
		if !v.expiresAt.After(now) {
			delete(t.cache, k)
		}
	}
}

func getPacket(n int) []byte {
	b := packetPool.Get().([]byte)
	if cap(b) < n {
		return make([]byte, n)
	}
	return b[:n]
}

func putPacket(b []byte) {
	// В пул кладём только “стандартные” буферы, чтобы не раздувать память.
	if cap(b) == defaultBufSize {
		packetPool.Put(b[:defaultBufSize])
	}
}

func StopTransfer() {
	transferMu.Lock()
	defer transferMu.Unlock()
	stopLocked()
}

func stopLocked() {
	if transferInst == nil {
		return
	}
	close(transferInst.stopCh)
	transferInst.wg.Wait()
	transferInst = nil
}
