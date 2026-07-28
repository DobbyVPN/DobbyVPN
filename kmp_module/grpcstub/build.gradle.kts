plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.protobuf)
}

kotlin { jvmToolchain(17) }

protobuf {
    protoc { artifact = libs.protoc.asProvider().get().toString() }
    plugins {
        create("grpc") { artifact = libs.protoc.gen.grpc.java.get().toString() }
        create("grpckt") { artifact = libs.protoc.gen.grpc.kotlin.get().toString() + ":jdk8@jar" }
    }
    generateProtoTasks {
        all().forEach {
            it.plugins {
                create("grpc")
                create("grpckt")
            }
            it.builtins { create("kotlin") }
        }
    }
}

dependencies {
    protobuf(project(":grpcprotos"))

    implementation(libs.kotlinx.coroutines.core)
    implementation(libs.grpc.stub)
    implementation(libs.grpc.protobuf)
    implementation(libs.protobuf.java.util)
    implementation(libs.protobuf.kotlin)
    implementation(libs.grpc.kotlin.stub)
    implementation(libs.grpc.netty)
    implementation("io.netty:netty-transport-native-epoll:4.1.127.Final")
    implementation("io.netty:netty-transport-native-kqueue:4.1.127.Final")
    runtimeOnly("io.netty:netty-transport-native-epoll:4.1.127.Final:linux-x86_64")
    runtimeOnly("io.netty:netty-transport-native-epoll:4.1.127.Final:linux-aarch_64")
    runtimeOnly("io.netty:netty-transport-native-kqueue:4.1.127.Final:osx-x86_64")
    runtimeOnly("io.netty:netty-transport-native-kqueue:4.1.127.Final:osx-aarch_64")

    testImplementation(kotlin("test"))
}
