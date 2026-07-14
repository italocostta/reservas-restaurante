<script setup lang="ts">
import type { Table } from '@/types/api'

defineProps<{ mesa: Table }>()
defineEmits<{ editar: []; alternar: [] }>()

// NÃO existe uma prop "ocupada" aqui, e a ausência é deliberada: saber se a mesa
// tem reserva hoje exigiria a agenda, que é a próxima fatia. Uma prop que nunca
// recebe valor é um `if` que nunca é verdade — código morto fingindo ser feature.
//
// A consequência é real e fica registrada: HOJE dá para desativar uma mesa com
// reserva confirmada em cima. O backend permite (o PATCH não olha reservas), e a
// reserva continua de pé — a mesa some da grade de disponibilidade sem levar a
// reserva junto. É um buraco de PRODUTO, não de código, e ele já existia antes
// desta tela. Vale virar débito na spec.
</script>

<template>
  <!-- Linha, não card. Um grid de cards com sombra e canto arredondado é o
       vocabulário de todo dashboard genérico e desperdiça a densidade que o passe
       exige: numa linha, o olho corre a coluna de capacidade sem saltar entre
       caixas. -->
  <li
    class="group border-ink-800 hover:bg-ink-900 relative flex items-center gap-5 border-b px-4 py-3.5 transition-colors"
    :class="{ 'opacity-45': !mesa.is_active }"
  >
    <!-- Talho vertical de estado: sage = de pé, vazio = desativada. É a primeira
         coisa que o olho pega ao correr a lista de cima a baixo, e não custa uma
         palavra de texto. -->
    <span
      class="absolute inset-y-0 left-0 w-0.5 transition-colors"
      :class="mesa.is_active ? 'bg-sage-500' : 'bg-transparent'"
      aria-hidden="true"
    ></span>

    <!-- A capacidade é o dado que o staff busca, então ela é o maior número da
         linha — mono, tabular, alinhada. Não um badge, não um ícone de cadeira. -->
    <div class="flex w-14 shrink-0 items-baseline gap-1">
      <span class="dado text-ink-100 text-2xl leading-none font-medium">
        {{ mesa.capacity }}
      </span>
      <span class="text-ink-500 text-[0.625rem] tracking-wide uppercase">lug</span>
    </div>

    <div class="min-w-0 flex-1">
      <p class="font-display text-ink-100 truncate text-base font-semibold">
        {{ mesa.name }}
      </p>
      <p v-if="!mesa.is_active" class="rotulo text-ink-500 mt-0.5">Fora de operação</p>
    </div>

    <!-- As ações só aparecem no hover/foco. Numa lista de 30 mesas, 60 botões
         permanentes competem com o dado — e o dado é o que a pessoa veio ler.
         focus-within mantém isso navegável no teclado, que é como o staff com
         pressa opera. -->
    <div
      class="flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100"
    >
      <button
        type="button"
        class="font-display text-ink-400 hover:text-ember-400 px-3 py-1.5 text-xs font-semibold tracking-wide uppercase transition-colors"
        @click="$emit('editar')"
      >
        Editar
      </button>

      <button
        type="button"
        class="font-display px-3 py-1.5 text-xs font-semibold tracking-wide uppercase transition-colors"
        :class="
          mesa.is_active
            ? 'text-ink-400 hover:text-blood-300'
            : 'text-ink-400 hover:text-sage-300'
        "
        @click="$emit('alternar')"
      >
        {{ mesa.is_active ? 'Desativar' : 'Reativar' }}
      </button>
    </div>
  </li>
</template>
