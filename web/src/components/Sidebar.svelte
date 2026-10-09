<script lang="ts">
  import Filters from "./Filters.svelte";
  import Legend from "./Legend.svelte";
  import Search from "./Search.svelte";
</script>

<aside class="sidebar">
  <Search />
  <Filters />
  <Legend />
</aside>

<style>
  .sidebar {
    width: 260px;
    flex: none;
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding: 12px;
    overflow-y: auto;
    background: var(--bg-panel);
    border-right: 1px solid var(--border);
  }
  @media (max-width: 700px) {
    .sidebar {
      width: 100%;
      max-height: 40vh;
      border-right: 0;
      border-bottom: 1px solid var(--border);
    }
  }
</style>
