package domain

type Category struct {
	ID          string
	Title       string
	Description string
	TopicIDs    []string
}

var Categories = []Category{
	{
		ID:          "go_core",
		Title:       "🟦 Go Core & Память",
		Description: "Базовые типы, слайсы, мапы, интерфейсы, аллокации и сборка мусора (GC)",
		TopicIDs: []string{
			"basic_types", "variables_scope", "constants_iota", "control_flow",
			"functions_closures", "pointers_references", "structs_methods",
			"arrays_slices", "strings_bytes_runes", "maps_internals",
			"interfaces_type_system", "error_handling", "generics", "generics_advanced",
			"packages_modules", "init_order", "visibility_export", "new_features",
			"memory_allocator", "escape_analysis", "garbage_collection", "go_memory_leaks",
			"unsafe", "reflection", "tricky_code",
		},
	},
	{
		ID:          "concurrency",
		Title:       "🟪 Конкурентность",
		Description: "Горутины, каналы, sync.Mutex, context, планировщик GMP и гонки данных",
		TopicIDs: []string{
			"goroutines", "channels", "concurrency_patterns", "sync_primitives",
			"context", "scheduler_gmp", "race_conditions", "memory_model",
			"lock_free", "netpoller",
		},
	},
	{
		ID:          "data_storage",
		Title:       "🟩 Данные и БД",
		Description: "PostgreSQL, транзакции, индексы, Redis, ORM, очереди и сериализация",
		TopicIDs: []string{
			"database_sql", "postgresql_advanced", "caching_redis", "orm",
			"message_queues", "json_xml_encoding", "io_files",
		},
	},
	{
		ID:          "networks_microservices",
		Title:       "🟨 Сети и микросервисы",
		Description: "HTTP, gRPC, Protobuf, WebSockets, Docker, Kubernetes и безопасность",
		TopicIDs: []string{
			"networking_basics", "http_server_client", "http2_http3", "grpc_protobuf",
			"websockets", "microservices", "docker_kubernetes", "cloud_native",
			"observability", "logging", "security", "cryptography",
		},
	},
	{
		ID:          "system_design",
		Title:       "🟧 System Design & Алгоритмы",
		Description: "Масштабирование, паттерны распределённых систем и алгоритмы",
		TopicIDs: []string{
			"system_design", "distributed_systems", "algorithms_data_structures",
			"leetcode_patterns", "compiler_internals",
		},
	},
	{
		ID:          "engineering_practices",
		Title:       "🟫 Инженерные практики",
		Description: "Тестирование, pprof, бенчмарки, CI/CD, Git, CGO и системные вызовы",
		TopicIDs: []string{
			"testing_basics", "testing_advanced", "benchmarks_profiling", "pprof_advanced",
			"profiling_advanced", "clean_architecture", "design_patterns_go", "git",
			"cicd", "debugging", "static_analysis", "code_generation", "go_generate",
			"cgo", "cgo_performance", "build_tags", "cross_compilation", "linkname_asm",
			"go_workspaces", "os_signals", "syscalls_os", "templates", "embedded",
			"wasm", "plugin_system", "runtime_internals",
		},
	},
}

func GetCategory(id string) (Category, bool) {
	for _, c := range Categories {
		if c.ID == id {
			return c, true
		}
	}
	return Category{}, false
}

func (c Category) FilterTopics(allTopics []Topic) []Topic {
	lookup := make(map[string]struct{}, len(c.TopicIDs))
	for _, tid := range c.TopicIDs {
		lookup[tid] = struct{}{}
	}

	var out []Topic
	for _, t := range allTopics {
		if _, ok := lookup[t.ID]; ok {
			out = append(out, t)
		}
	}
	return out
}
