# web — frontend do sistema de reservas

SPA staff-only. Vue 3 + TypeScript + Pinia + Vue Router + Tailwind 4, via Vite.

O racional das decisões está na **seção 15** da `spec-sistema-reservas.md` (raiz do
repo). Este README só diz como rodar.

## Rodar

Precisa da **API em Go no ar**, na raiz do repo:

```bash
go run ./cmd/api          # :8080
```

Depois, aqui:

```bash
npm install
npm run dev               # :5173
```

A porta 5173 é obrigatória (`strictPort`): é a única origem que o
`CORS_ALLOWED_ORIGIN` do backend libera. Se ela estiver ocupada, o Vite **falha** em
vez de cair na 5174 — o que produziria um erro de CORS bem menos óbvio.

Não há proxy para a API: o browser fala direto com `localhost:8080`, usando o CORS
de verdade. Esconder isso atrás de um proxy do Vite adiaria a descoberta de um CORS
quebrado para produção, onde o proxy não existe.

## Comandos

| Comando | O quê |
|---|---|
| `npm run dev` | dev server com HMR |
| `npm run check` | **typecheck** — `vue-tsc -b --noEmit` |
| `npm run build` | typecheck + bundle de produção |

**O `-b` do `check` não é opcional.** O `tsconfig.json` da raiz tem `"files": []` e só
referencia os outros dois — sem build mode, o `vue-tsc` checa **zero arquivos** e sai
verde com erros no código. Foi assim que três erros reais passaram batidos uma vez.

## Configuração

```bash
cp .env.example .env
```

Só existe uma variável: `VITE_API_URL`.

**O expediente do restaurante NÃO fica aqui.** `SERVICE_START`/`SERVICE_END`/
`SERVICE_TZ` vêm de `GET /service-hours` — é a razão de aquele endpoint existir
(seção 15 da spec). Copiar `18:00` para um `.env` do frontend seria a mesma verdade
em dois processos, com nenhum teste vigiando a divergência.

## Estrutura

```
src/
├─ api/
│  ├─ client.ts     fetch + ApiError/NetworkError. O ÚNICO lugar que traduz {"error": "..."}
│  └─ errors.ts     erro → texto que o staff lê
├─ types/api.ts     tipos do domínio, escritos à mão (débito 16 da spec)
├─ stores/          Pinia
├─ components/
├─ views/
└─ style.css        @theme: paleta, fontes, o vocabulário visual inteiro
```
