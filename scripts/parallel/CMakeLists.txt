cmake_minimum_required(VERSION 3.24)
project(pakr_parallel CXX)

set(NATIVE_SOURCE "${CMAKE_CURRENT_SOURCE_DIR}/../../internal/native")
add_library(pakr_parallel STATIC "${NATIVE_SOURCE}/parallel/parallel.cc")
target_compile_features(pakr_parallel PRIVATE cxx_std_17)
target_compile_definitions(pakr_parallel PRIVATE WITH_ISAL)

# rapidgzip's compile-time lookup tables exceed Clang's default constexpr budget.
target_compile_options(pakr_parallel PRIVATE -fconstexpr-steps=100000000)
target_include_directories(pakr_parallel PRIVATE
	"${NATIVE_SOURCE}"
	"${CMAKE_INSTALL_PREFIX}/include"
	"${SOURCE}/src"
	"${SOURCE}/src/core"
	"${SOURCE}/src/core/filereader"
	"${SOURCE}/src/huffman"
	"${SOURCE}/src/indexed_bzip2"
	"${SOURCE}/src/rapidgzip"
	"${SOURCE}/src/rapidgzip/gzip"
	"${SOURCE}/src/rapidgzip/huffman"
	"${SOURCE}/src/rapidgzip/blockfinder"
	"${SOURCE}/src/rapidgzip/chunkdecoding"
)
install(TARGETS pakr_parallel ARCHIVE DESTINATION lib)
