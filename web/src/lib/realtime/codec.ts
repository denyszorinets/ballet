import { decode, encode } from '@msgpack/msgpack';

export const SUBPROTOCOL_MSGPACK = 'ballet.v1.msgpack';
export const SUBPROTOCOL_JSON = 'ballet.v1.json';

/** JSON-RPC 2.0 envelope (also used, as a map, for MessagePack). */
export interface Envelope {
	jsonrpc: '2.0';
	id?: number;
	method?: string;
	params?: unknown;
	result?: unknown;
	error?: { code: number; message: string };
}

export interface Codec {
	encode(m: Envelope): string | Uint8Array<ArrayBuffer>;
	decode(data: unknown): Envelope;
}

const msgpackCodec: Codec = {
	encode: (m) => new Uint8Array(encode(m)),
	decode: (data) => {
		const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : (data as Uint8Array);
		return decode(bytes) as Envelope;
	}
};

const jsonCodec: Codec = {
	encode: (m) => JSON.stringify(m),
	decode: (data) => JSON.parse(data as string) as Envelope
};

/** Returns the codec for a negotiated subprotocol. */
export function codecFor(subprotocol: string): Codec {
	switch (subprotocol) {
		case SUBPROTOCOL_MSGPACK:
			return msgpackCodec;
		case SUBPROTOCOL_JSON:
			return jsonCodec;
	}
	throw new Error(`unsupported subprotocol "${subprotocol}"`);
}
