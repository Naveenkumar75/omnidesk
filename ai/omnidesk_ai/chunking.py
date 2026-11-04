def chunk_text(text: str, max_chars: int = 700) -> list[str]:
    """Split on blank lines, then pack paragraphs into chunks of <= max_chars.

    Paragraphs longer than max_chars are split on sentence-ish boundaries so a
    chunk never exceeds the limit (unless a single word does).
    """
    if max_chars < 50:
        raise ValueError("max_chars must be at least 50")

    pieces: list[str] = []
    for para in (p.strip() for p in text.split("\n\n")):
        if not para:
            continue
        if len(para) <= max_chars:
            pieces.append(para)
            continue
        current = ""
        for sentence in para.replace("\n", " ").split(". "):
            sentence = sentence.strip()
            if not sentence:
                continue
            candidate = f"{current}. {sentence}" if current else sentence
            if len(candidate) <= max_chars:
                current = candidate
            else:
                if current:
                    pieces.append(current)
                while len(sentence) > max_chars:
                    cut = sentence.rfind(" ", 0, max_chars)
                    cut = cut if cut > 0 else max_chars
                    pieces.append(sentence[:cut])
                    sentence = sentence[cut:].lstrip()
                current = sentence
        if current:
            pieces.append(current)

    chunks: list[str] = []
    buf = ""
    for piece in pieces:
        candidate = f"{buf}\n\n{piece}" if buf else piece
        if len(candidate) <= max_chars:
            buf = candidate
        else:
            if buf:
                chunks.append(buf)
            buf = piece
    if buf:
        chunks.append(buf)
    return chunks
