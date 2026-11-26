class PCMProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.frameSamples = 960;
    this.out = new Int16Array(this.frameSamples);
    this.outPos = 0;
    this.sumSq = 0;
    this.baseRatio = sampleRate / 48000;
    this.trimPpm = 0;
    this.ratio = this.baseRatio;
    this.pos = 0;
    this.prev = 0;
    this.port.onmessage = (e) => {
      if (typeof e.data.trimPpm === 'number') {
        this.trimPpm = e.data.trimPpm;
        this.ratio = this.baseRatio / (1 + this.trimPpm / 1e6);
      }
    };
  }

  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (!ch || ch.length === 0) return true;

    if (this.ratio === 1) {
      for (let i = 0; i < ch.length; i++) this.push(ch[i]);
      this.prev = ch[ch.length - 1];
      this.pos = 0;
      return true;
    }

    let p = this.pos;
    while (p < ch.length) {
      const idx = Math.floor(p);
      const frac = p - idx;
      const a = idx === 0 ? this.prev : ch[idx - 1];
      const b = ch[idx];
      this.push(a + (b - a) * frac);
      p += this.ratio;
    }
    this.prev = ch[ch.length - 1];
    this.pos = p - ch.length;
    return true;
  }

  push(v) {
    const s = Math.max(-1, Math.min(1, v));
    this.sumSq += s * s;
    this.out[this.outPos++] = s < 0 ? s * 0x8000 : s * 0x7fff;
    if (this.outPos === this.frameSamples) {
      const level = Math.sqrt(this.sumSq / this.frameSamples);
      const buf = this.out.buffer.slice(0);
      this.port.postMessage({ pcm: buf, level }, [buf]);
      this.outPos = 0;
      this.sumSq = 0;
    }
  }
}

registerProcessor('pcm-processor', PCMProcessor);