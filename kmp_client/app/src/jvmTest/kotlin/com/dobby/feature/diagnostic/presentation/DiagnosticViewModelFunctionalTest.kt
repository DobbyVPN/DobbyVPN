package com.dobby.feature.diagnostic.presentation

import com.dobby.feature.diagnostic.domain.IpRepository
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import com.dobby.feature.diagnostic.domain.IpData as DomainIpData

@OptIn(ExperimentalCoroutinesApi::class)
class DiagnosticViewModelFunctionalTest {

    private val testDispatcher = StandardTestDispatcher()

    @BeforeTest
    fun setUp() {
        Dispatchers.setMain(testDispatcher)
    }

    @AfterTest
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun `reloadIpData sets loading state then success`() = runTest {
        val fakeRepo = FakeIpRepository(
            ipData = DomainIpData(ip = "1.2.3.4", city = "Moscow", country = "Russia")
        )
        val vm = DiagnosticViewModel(fakeRepo, testDispatcher)

        assertEquals(IpData.EMPTY, vm.uiState.value.ipData)

        vm.reloadIpData()

        assertEquals(IpData.LOADING, vm.uiState.value.ipData)

        advanceUntilIdle()

        assertEquals("1.2.3.4", vm.uiState.value.ipData.ip)
        assertEquals("Moscow", vm.uiState.value.ipData.city)
        assertEquals("Russia", vm.uiState.value.ipData.country)
    }

    @Test
    fun `reloadIpData sets failed on exception`() = runTest {
        val fakeRepo = FakeIpRepository(shouldThrow = true)
        val vm = DiagnosticViewModel(fakeRepo, testDispatcher)

        vm.reloadIpData()
        advanceUntilIdle()

        assertEquals("Failed", vm.uiState.value.ipData.ip)
        assertEquals("", vm.uiState.value.ipData.city)
        assertEquals("", vm.uiState.value.ipData.country)
    }

    @Test
    fun `reloadDnsIpData sets loading state then success`() = runTest {
        val fakeRepo = FakeIpRepository(
            hostnameIpData = DomainIpData(ip = "5.6.7.8", city = "Berlin", country = "Germany")
        )
        val vm = DiagnosticViewModel(fakeRepo, testDispatcher)

        assertEquals(IpData.EMPTY, vm.uiState.value.dnsData)

        vm.reloadDnsIpData("example.com")

        assertEquals(IpData.LOADING, vm.uiState.value.dnsData)

        advanceUntilIdle()

        assertEquals("5.6.7.8", vm.uiState.value.dnsData.ip)
        assertEquals("Berlin", vm.uiState.value.dnsData.city)
        assertEquals("Germany", vm.uiState.value.dnsData.country)
    }

    @Test
    fun `reloadDnsIpData sets failed on exception`() = runTest {
        val fakeRepo = FakeIpRepository(shouldThrowHostname = true)
        val vm = DiagnosticViewModel(fakeRepo, testDispatcher)

        vm.reloadDnsIpData("example.com")
        advanceUntilIdle()

        assertEquals("Failed", vm.uiState.value.dnsData.ip)
        assertEquals("", vm.uiState.value.dnsData.city)
        assertEquals("", vm.uiState.value.dnsData.country)
    }

    @Test
    fun `reloadIpData preserves dnsData state`() = runTest {
        val fakeRepo = FakeIpRepository(
            ipData = DomainIpData(ip = "1.2.3.4", city = "Moscow", country = "Russia"),
            hostnameIpData = DomainIpData(ip = "5.6.7.8", city = "Berlin", country = "Germany")
        )
        val vm = DiagnosticViewModel(fakeRepo, testDispatcher)

        vm.reloadDnsIpData("example.com")
        advanceUntilIdle()

        val dnsDataBefore = vm.uiState.value.dnsData

        vm.reloadIpData()
        advanceUntilIdle()

        assertEquals(dnsDataBefore, vm.uiState.value.dnsData)
        assertEquals("1.2.3.4", vm.uiState.value.ipData.ip)
    }

    @Test
    fun `reloadDnsIpData preserves ipData state`() = runTest {
        val fakeRepo = FakeIpRepository(
            ipData = DomainIpData(ip = "1.2.3.4", city = "Moscow", country = "Russia"),
            hostnameIpData = DomainIpData(ip = "5.6.7.8", city = "Berlin", country = "Germany")
        )
        val vm = DiagnosticViewModel(fakeRepo, testDispatcher)

        vm.reloadIpData()
        advanceUntilIdle()

        val ipDataBefore = vm.uiState.value.ipData

        vm.reloadDnsIpData("example.com")
        advanceUntilIdle()

        assertEquals(ipDataBefore, vm.uiState.value.ipData)
        assertEquals("5.6.7.8", vm.uiState.value.dnsData.ip)
    }

    @Test
    fun `reloadDnsIpData passes hostname to repository`() = runTest {
        val fakeRepo = FakeIpRepository(
            hostnameIpData = DomainIpData(ip = "1.1.1.1", city = "", country = "")
        )
        val vm = DiagnosticViewModel(fakeRepo, testDispatcher)

        vm.reloadDnsIpData("custom-host.example.org")
        advanceUntilIdle()

        assertEquals("custom-host.example.org", fakeRepo.lastHostnameQueried)
    }
}

private class FakeIpRepository(
    private val ipData: DomainIpData = DomainIpData(ip = "0.0.0.0", city = "", country = ""),
    private val hostnameIpData: DomainIpData = DomainIpData(ip = "0.0.0.0", city = "", country = ""),
    private val shouldThrow: Boolean = false,
    private val shouldThrowHostname: Boolean = false,
) : IpRepository {
    var lastHostnameQueried: String? = null

    override fun getIpData(): DomainIpData {
        if (shouldThrow) throw RuntimeException("Network error")
        return ipData
    }

    override fun getHostnameIpData(hostname: String): DomainIpData {
        lastHostnameQueried = hostname
        if (shouldThrowHostname) throw RuntimeException("DNS error")
        return hostnameIpData
    }
}
